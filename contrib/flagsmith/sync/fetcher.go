// Package flagsmith is the sync provider for Flagsmith: a fetcher that reads the environment document of one
// environment from the Flagsmith API (UC-019). The engine module of Flagsmith keeps and evaluates the document.
//
// A source is the API base, such as https://edge.api.flagsmith.com/api/v1/, with the provider name flagsmith, or the
// same address with the prefix flagsmith+ (flagsmith+https://...), which selects this provider on the command line.
// The environment's key goes into the X-Environment-Key header, read from an environment variable or a file through
// headersFrom, so that it is neither in the configuration nor on the command line.
//
// The fetcher is triggered by the core's poller, or by any fetch trigger a source lists, such as an SSE stream next to
// the poller.
package flagsmith

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"go.uber.org/zap"
)

const (
	// Provider is the provider name of Flagsmith sources.
	Provider = "flagsmith"

	// KeyHeader is the header that carries the environment's key.
	KeyHeader = "X-Environment-Key"

	// prefix selects this provider on the command line, in front of the address of the API.
	prefix = "flagsmith+"

	documentPath = "environment-document/"

	// maxPages and maxPageBytes bound what a source can make the fetcher read.
	maxPages     = 10000
	maxPageBytes = 128 << 20
)

// Fetcher reads the environment document of one environment: a conditional read in the sense of the sync contract,
// where the source version is the digest of the pages it read.
type Fetcher struct {
	base    *url.URL
	headers map[string]string
	client  *http.Client
	logger  *logger.Logger
}

var (
	_ sync.Fetcher = (*Fetcher)(nil)
	_ io.Closer    = (*Fetcher)(nil)
)

// NewFetcher creates the fetcher of a source. Its headers must already be resolved (sync.ResolveHeaders) and hold the
// environment's key.
func NewFetcher(config sync.SourceConfig, log *logger.Logger) (*Fetcher, error) {
	base, err := parseBase(config.URI)
	if err != nil {
		return nil, err
	}
	headers := make(map[string]string, len(config.Headers)+1)
	for name, value := range config.Headers {
		headers[http.CanonicalHeaderKey(name)] = value
	}
	if headers[KeyHeader] == "" {
		return nil, fmt.Errorf("source %q: the %s header is missing: set it with headersFrom", redact(base), KeyHeader)
	}

	return &Fetcher{
		base:    base,
		headers: headers,
		client: &http.Client{
			Timeout: time.Duration(config.TimeoutS) * time.Second,
			// a redirect would send the key to another address
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		logger: log.WithFields(zap.String("component", "sync"), zap.String("sync", Provider)),
	}, nil
}

// parseBase reads the address of the API from a source's URI, with a trailing slash.
func parseBase(uri string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimPrefix(uri, prefix))
	if err != nil {
		return nil, fmt.Errorf("a flagsmith source is the address of the API: %s", uriError(err))
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("source %q: a flagsmith source is an http or https address", redact(u))
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	u.RawQuery, u.Fragment = "", ""
	return u, nil
}

// Fetch reads every page of the environment document and returns it as one document. The pages carry the identity
// overrides in chunks, linked with rel="next"; the document is the first page with the overrides of all pages. A
// source that refuses the key (401 or 403) is reported with sync.ErrSourceRefused.
func (f *Fetcher) Fetch(ctx context.Context, previous string) (sync.FetchResult, error) {
	first := f.base.JoinPath(documentPath)
	digest := sha256.New()
	var document map[string]json.RawMessage
	var overrides []json.RawMessage
	sawOverrides := false

	seen := map[string]bool{}
	for next, pages := first, 0; next != nil; pages++ {
		if pages == maxPages {
			return sync.FetchResult{}, fmt.Errorf("the environment document has more than %d pages", maxPages)
		}
		if seen[next.String()] {
			return sync.FetchResult{}, errors.New("the pages of the environment document loop")
		}
		seen[next.String()] = true

		body, link, err := f.get(ctx, next)
		if err != nil {
			return sync.FetchResult{}, err
		}
		digest.Write(body)

		var current map[string]json.RawMessage
		if err := json.Unmarshal(body, &current); err != nil || current == nil {
			return sync.FetchResult{}, fmt.Errorf("page %d of the environment document is not a JSON object", pages+1)
		}
		if raw, ok := current["identity_overrides"]; ok {
			var chunk []json.RawMessage
			if err := json.Unmarshal(raw, &chunk); err != nil {
				return sync.FetchResult{}, fmt.Errorf("page %d of the environment document: identity_overrides is not a list", pages+1)
			}
			overrides, sawOverrides = append(overrides, chunk...), true
		}
		if document == nil {
			document = current
		}

		if next, err = f.nextPage(next, link); err != nil {
			return sync.FetchResult{}, err
		}
		f.logger.Debug(fmt.Sprintf("read page %d of the environment document", pages+1))
	}

	version := hex.EncodeToString(digest.Sum(nil))
	if previous != "" && version == previous {
		return sync.FetchResult{Unchanged: true, Version: version}, nil
	}
	if sawOverrides {
		combined, err := json.Marshal(overrides)
		if err != nil {
			return sync.FetchResult{}, fmt.Errorf("combining the pages of the environment document: %w", err)
		}
		document["identity_overrides"] = combined
	}
	data, err := json.Marshal(document)
	if err != nil {
		return sync.FetchResult{}, fmt.Errorf("combining the pages of the environment document: %w", err)
	}
	return sync.FetchResult{Data: data, Extension: ".json", ContentType: "application/json", Version: version}, nil
}

// get reads one page and returns its body and Link headers.
func (f *Fetcher) get(ctx context.Context, u *url.URL) (body []byte, link []string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, nil, fmt.Errorf("creating the request for %s: %s", redact(u), uriError(err))
	}
	req.Header.Set("Accept", "application/json")
	for name, value := range f.headers {
		req.Header.Set(name, value)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		// a url.Error repeats the address, which may hold a query or credentials: report the cause only
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, nil, fmt.Errorf("calling %s: %w", redact(u), err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, nil, fmt.Errorf("the source answered %d: %w", resp.StatusCode, sync.ErrSourceRefused)
	case resp.StatusCode != http.StatusOK:
		return nil, nil, fmt.Errorf("the source answered %d", resp.StatusCode)
	}

	body, err = io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", redact(u), err)
	}
	if len(body) > maxPageBytes {
		return nil, nil, fmt.Errorf("a page of the environment document is larger than %d bytes", maxPageBytes)
	}
	return body, resp.Header.Values("Link"), nil
}

var (
	linkEntry = regexp.MustCompile(`<([^>]*)>([^<]*)`)
	relParam  = regexp.MustCompile(`(?i)\brel\s*=\s*"?([^";,]+)"?`)
)

// nextPage returns the page the Link headers name with rel="next", or nil when there is none. The page has to be on the
// address of the API: the key never goes anywhere else.
func (f *Fetcher) nextPage(current *url.URL, link []string) (*url.URL, error) {
	for _, header := range link {
		for _, entry := range linkEntry.FindAllStringSubmatch(header, -1) {
			rel := relParam.FindStringSubmatch(entry[2])
			if rel == nil || !contains(strings.Fields(strings.ToLower(rel[1])), "next") {
				continue
			}
			target, err := url.Parse(strings.TrimSpace(entry[1]))
			if err != nil {
				return nil, fmt.Errorf("the link to the next page is not an address: %s", uriError(err))
			}
			target = current.ResolveReference(target)
			if target.Scheme != f.base.Scheme || target.Host != f.base.Host {
				return nil, fmt.Errorf("the next page of the environment document is on another address (%s://%s)", target.Scheme, target.Host)
			}
			return target, nil
		}
	}
	return nil, nil
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// Close releases the connections of the fetcher.
func (f *Fetcher) Close() error {
	f.client.CloseIdleConnections()
	return nil
}

// redact is an address without credentials and query, for messages.
func redact(u *url.URL) string {
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

// uriError is the reason of a parse error without the text it parsed, which may hold credentials.
func uriError(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err.Error()
	}
	return err.Error()
}

// Factory creates the fetchers of Flagsmith sources.
type Factory struct{}

var _ sync.FetcherFactory = Factory{}

// Type is the provider name.
func (Factory) Type() string { return Provider }

// Schemes are the prefixes that select this provider on the command line.
func (Factory) Schemes() []string { return []string{prefix + "https://", prefix + "http://"} }

// ParseURI claims the addresses with the prefix flagsmith+ and keeps them as given. Plain http and https addresses
// belong to the http sync provider, so a source in the configuration file names this provider.
func (Factory) ParseURI(uri string) (sync.SourceConfig, bool) {
	for _, scheme := range (Factory{}).Schemes() {
		if strings.HasPrefix(uri, scheme) {
			return sync.SourceConfig{URI: uri, Provider: Provider}, true
		}
	}
	return sync.SourceConfig{}, false
}

// CreateFetcher creates the fetcher of a source. The environment's key comes from headersFrom, and a variable or file
// that cannot be read stops the source.
func (Factory) CreateFetcher(settings sync.Settings) (sync.Fetcher, error) {
	headers, err := sync.ResolveHeaders(settings.Source)
	if err != nil {
		return nil, err
	}
	config := settings.Source
	config.Headers, config.HeadersFrom = headers, nil
	return NewFetcher(config, settings.Logger)
}

// NewFactories returns the factories of the Flagsmith sync component, as a generated main of a distribution calls it.
func NewFactories() []sync.Factory { return []sync.Factory{Factory{}} }
