// Package sse is a fetch trigger for sources that announce a change on a server-sent event stream. The trigger signals
// once for each event the stream dispatches, so the fetching sync provider of the source fetches it. It reads no
// event: what changed is for the fetcher to find out.
//
// The stream is best effort. The trigger connects in Run, never at startup, so a stream that is unreachable or refused
// neither stops the distribution nor delays readiness: Run returns an error, and the fetching sync provider runs it
// again after a backoff. A source that sets `triggers` replaces the defaults, including the poller, so list
// `{"type": "poller"}` next to `{"type": "sse", ...}` to keep fetching on a schedule.
package sse

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
)

// Type is the type of the trigger in the `triggers` of a source.
const Type = "sse"

// The settings of the trigger.
const (
	settingURL     = "url"
	settingHeaders = "headers"
)

// Factory creates the SSE fetch trigger of a source. Its settings are the url of the stream and the headers to send
// with the request, which carry a credential where the stream needs one.
type Factory struct{}

var _ sync.TriggerFactory = Factory{}

// NewFactories returns the factories of this module, for a generated main to pass to the runtime.
func NewFactories() []sync.Factory { return []sync.Factory{Factory{}} }

// Type implements sync.Factory.
func (Factory) Type() string { return Type }

// CreateTrigger checks the settings and creates the trigger. It does not connect.
func (Factory) CreateTrigger(settings sync.Settings) (sync.Trigger, error) {
	target, headers, err := parseSettings(settings.Config)
	if err != nil {
		return nil, fmt.Errorf("the %s fetch trigger of %q: %w", Type, settings.Source.URI, err)
	}
	return &trigger{
		url:     target.String(),
		shown:   describe(target),
		headers: headers,
		log:     settings.Logger,
		client:  newClient(),
	}, nil
}

// parseSettings reads and checks the settings. An error names the setting and, for a header, its name: never a value.
func parseSettings(config map[string]any) (*url.URL, map[string]string, error) {
	var unknown []string
	for key := range config {
		if key != settingURL && key != settingHeaders {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, nil, fmt.Errorf("unknown setting %q (allowed: %s, %s)", unknown[0], settingHeaders, settingURL)
	}

	raw, ok := config[settingURL]
	if !ok {
		return nil, nil, fmt.Errorf("the setting %q is mandatory: the address of the event stream", settingURL)
	}
	text, ok := raw.(string)
	if !ok {
		return nil, nil, fmt.Errorf("the setting %q must be text", settingURL)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil, fmt.Errorf("the setting %q is mandatory: the address of the event stream", settingURL)
	}
	target, err := url.Parse(text)
	if err != nil {
		// the parse error quotes the address, which may carry a token in its query
		return nil, nil, fmt.Errorf("the setting %q is not a valid address", settingURL)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, nil, fmt.Errorf("the setting %q must be an http or https address", settingURL)
	}
	if target.Hostname() == "" {
		return nil, nil, fmt.Errorf("the setting %q needs a host", settingURL)
	}

	headers, err := parseHeaders(config[settingHeaders])
	if err != nil {
		return nil, nil, err
	}
	return target, headers, nil
}

func parseHeaders(raw any) (map[string]string, error) {
	switch h := raw.(type) {
	case nil:
		return nil, nil
	case map[string]string:
		return checkHeaders(h)
	case map[string]any:
		out := make(map[string]string, len(h))
		for name, value := range h {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("the header %q must be text", name)
			}
			out[name] = text
		}
		return checkHeaders(out)
	default:
		return nil, fmt.Errorf("the setting %q must be a mapping of header names to text", settingHeaders)
	}
}

func checkHeaders(headers map[string]string) (map[string]string, error) {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "" {
			return nil, errors.New("a header name is empty")
		}
		if strings.ContainsAny(name, " \t\r\n:") || strings.ContainsAny(headers[name], "\r\n") {
			return nil, fmt.Errorf("the header %q is not valid: a name holds no space or colon, a value no line break", name)
		}
	}
	return headers, nil
}

// describe is the address of the stream as an error or a log line shows it: without credentials and query, which
// may carry a token.
func describe(target *url.URL) string {
	return target.Scheme + "://" + target.Host + target.EscapedPath()
}

// newClient creates the client of one trigger. A stream is long-lived, so the client sets no overall timeout, only one
// for the response to start. It follows no redirect: the request carries headers that may hold a credential, which
// must not reach another address.
func newClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// trigger is the SSE fetch trigger. It is not an InitializingTrigger: nothing connects before Run.
type trigger struct {
	url     string
	shown   string
	headers map[string]string
	log     *logger.Logger
	client  *http.Client
}

var _ sync.Trigger = (*trigger)(nil)

// Run connects to the stream and signals once for each event it dispatches, until ctx is done, when it returns nil. A
// stream that cannot be reached, answers anything but an event stream, breaks or is closed by the server is an error,
// so that the fetching sync provider runs Run again after a backoff.
func (t *trigger) Run(ctx context.Context, signal func()) error {
	defer t.client.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url, nil)
	if err != nil {
		// the error quotes the whole address
		return fmt.Errorf("cannot request the event stream at %s", t.shown)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for name, value := range t.headers {
		req.Header.Set(name, value)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		// the error of the client quotes the whole address; the cause is enough
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("cannot connect to the event stream at %s: %w", t.shown, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the event stream at %s answered %s", t.shown, resp.Status)
	}
	if mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mediaType != "text/event-stream" {
		return fmt.Errorf("the event stream at %s answered with the content type %q, not text/event-stream", t.shown, mediaType)
	}
	t.debug("connected to the event stream at " + t.shown)

	err = readEvents(resp.Body, func(event) { signal() })
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("the event stream at %s broke: %w", t.shown, err)
	}
	return fmt.Errorf("the event stream at %s was closed by the server", t.shown)
}

func (t *trigger) debug(msg string) {
	if t.log != nil {
		t.log.Debug(msg)
	}
}
