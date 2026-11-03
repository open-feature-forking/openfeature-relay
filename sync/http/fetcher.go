package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/utils"
	"go.uber.org/zap"
)

// Provider is the provider name of http sources.
const Provider = "http"

// Fetcher reads a flag configuration with a conditional GET. Its source version holds the digest of the body and the
// ETag the server gave: a new ETag for the same body is unchanged.
type Fetcher struct {
	uri        string
	logger     *logger.Logger
	authHeader string
	headers    map[string]string
	timeoutS   time.Duration

	client          Client
	oauthCredential *oauthCredentialHandler
}

var _ sync.Fetcher = (*Fetcher)(nil)

// NewFetcher creates the fetcher of an http source. The configuration sets the authorization header, headers, OAuth
// credentials and timeout.
func NewFetcher(config sync.SourceConfig, log *logger.Logger) *Fetcher {
	var oauthCredential *oauthCredentialHandler
	if config.OAuth != nil {
		oauthCredential = &oauthCredentialHandler{
			clientId:           config.OAuth.ClientID,
			clientSecret:       config.OAuth.ClientSecret,
			tokenUrl:           config.OAuth.TokenURL,
			folderSource:       config.OAuth.Folder,
			reloadDelaySeconds: config.OAuth.ReloadDelayS,
			lastUpdate:         time.Now(),
		}
	}

	canonicalHeaders := make(map[string]string, len(config.Headers))
	for k, v := range config.Headers {
		canonicalHeaders[http.CanonicalHeaderKey(k)] = v
	}

	return &Fetcher{
		uri: config.URI,
		logger: log.WithFields(
			zap.String("component", "sync"),
			zap.String("sync", Provider),
		),
		authHeader:      config.AuthHeader,
		headers:         canonicalHeaders,
		timeoutS:        time.Duration(config.TimeoutS),
		oauthCredential: oauthCredential,
	}
}

// joinVersion and splitVersion build and read the source version: "<digest> <etag>".
func joinVersion(digest, etag string) string { return digest + " " + etag }

func splitVersion(version string) (digest, etag string) {
	digest, etag, _ = strings.Cut(version, " ")
	return digest, etag
}

// Fetch reads the source. With a previous version it sends the ETag in If-None-Match. A 304, and a body that has the
// digest it had, are reported as unchanged; so is an empty body, which is not a flag configuration.
func (f *Fetcher) Fetch(ctx context.Context, previous string) (sync.FetchResult, error) {
	if f.uri == "" {
		return sync.FetchResult{}, errors.New("no HTTP URL string set")
	}
	previousDigest, previousETag := splitVersion(previous)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.uri, http.NoBody)
	if err != nil {
		return sync.FetchResult{}, fmt.Errorf("error creating request for url %s: %w", f.uri, err)
	}
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Accept", "application/yaml")
	if f.authHeader != "" {
		req.Header.Set("Authorization", f.authHeader)
	}
	if previousETag != "" {
		req.Header.Set("If-None-Match", previousETag)
	}
	for key, value := range f.headers {
		if http.CanonicalHeaderKey(key) == "Host" {
			req.Host = value
		} else {
			req.Header.Set(key, value)
		}
	}

	resp, err := f.getClient().Do(req)
	if err != nil {
		return sync.FetchResult{}, fmt.Errorf("error calling endpoint %s: %w", f.uri, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			f.logger.Error(fmt.Sprintf("error closing the response body: %s", err.Error()))
		}
	}()

	if resp.StatusCode == http.StatusNotModified {
		f.logger.Debug("no changes detected")
		return sync.FetchResult{Unchanged: true}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return sync.FetchResult{}, fmt.Errorf("error fetching from url %s: %s", f.uri, resp.Status)
	}

	etag := resp.Header.Get("ETag")
	if etag == "" {
		etag = previousETag
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return sync.FetchResult{}, fmt.Errorf("unable to read body to bytes: %w", err)
	}
	if len(body) == 0 {
		f.logger.Debug("configuration deleted")
		return sync.FetchResult{Unchanged: true, Version: joinVersion(previousDigest, etag)}, nil
	}

	digest := utils.GenerateSha(body)
	version := joinVersion(digest, etag)
	if previousDigest != "" && digest == previousDigest {
		return sync.FetchResult{Unchanged: true, Version: version}, nil
	}
	return sync.FetchResult{
		Data:        body,
		Extension:   getFileExtensions(f.uri),
		ContentType: resp.Header.Get("Content-Type"),
		Version:     version,
	}, nil
}

// getClient gives the HTTP client, creating it, and again once OAuth credentials need to be reloaded.
func (f *Fetcher) getClient() Client {
	if f.oauthCredential != nil {
		delta := time.Since(f.oauthCredential.lastUpdate).Seconds()
		if delta <= float64(f.oauthCredential.reloadDelaySeconds) && f.client != nil {
			return f.client
		}
	} else if f.client != nil {
		return f.client
	}
	client := &http.Client{Timeout: time.Second * f.timeoutS}
	if f.oauthCredential != nil {
		if c, err := f.oauthCredential.getOAuthClient(); err == nil {
			client = c
			client.Timeout = time.Second * f.timeoutS
			f.oauthCredential.lastUpdate = time.Now()
		} else {
			f.logger.Error(fmt.Sprintf("Cannot init OAuth. Default to normal HTTP client: %v", err))
		}
	}
	f.client = client
	return client
}

// Factory creates the fetchers of http sources.
type Factory struct{}

var (
	_ sync.FetcherFactory = Factory{}
	_ sync.SourceTyper    = Factory{}
)

// Type is the provider name.
func (Factory) Type() string { return Provider }

// SourceType is the type the sync metrics record.
func (Factory) SourceType() string { return sync.SourceHTTP }

// Schemes are the prefixes of http sources.
func (Factory) Schemes() []string { return []string{"http://", "https://"} }

// ParseURI claims http and https URIs as they are.
func (Factory) ParseURI(uri string) (sync.SourceConfig, bool) {
	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
		return sync.SourceConfig{URI: uri, Provider: Provider}, true
	}
	return sync.SourceConfig{}, false
}

// CreateFetcher creates the fetcher of a source.
func (Factory) CreateFetcher(settings sync.Settings) (sync.Fetcher, error) {
	return NewFetcher(settings.Source, settings.Logger), nil
}

// NewFactories returns the factories of the HTTP sync component, as a generated main of a distribution calls it.
func NewFactories() []sync.Factory { return []sync.Factory{Factory{}} }
