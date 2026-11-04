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
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	gosync "sync"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/secret"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
)

// Type is the type of the trigger in the `triggers` of a source.
const Type = "sse"

// The settings of the trigger.
const (
	settingURL         = "url"
	settingHeaders     = "headers"
	settingHeadersFrom = "headersFrom"
	settingIdleTimeout = "idleTimeout"
)

// Factory creates the SSE fetch trigger of a source. Its settings are:
//   - url, the address of the stream;
//   - headers, to send with the request;
//   - headersFrom, headers whose values are read from an environment variable or a file, as {"env": "NAME"} or
//     {"file": "PATH"}, so that a credential is in neither the configuration nor the command line. They are merged
//     over headers, as the headersFrom of a source is (sync.ResolveHeaders);
//   - idleTimeout, a duration such as "5m": a stream that sends no byte for that long is closed and connected again.
//     Servers send comments as keepalives for this; without it, a connection that dies silently stays open.
type Factory struct{}

var _ sync.TriggerFactory = Factory{}

// NewFactories returns the factories of this module, for a generated main to pass to the runtime.
func NewFactories() []sync.Factory { return []sync.Factory{Factory{}} }

// Type implements sync.Factory.
func (Factory) Type() string { return Type }

// CreateTrigger checks the settings and creates the trigger. It does not connect.
func (Factory) CreateTrigger(settings sync.Settings) (sync.Trigger, error) {
	parsed, err := parseSettings(settings.Config)
	if err != nil {
		return nil, fmt.Errorf("the %s fetch trigger of %q: %w", Type, settings.Source.URI, err)
	}
	// the shared resolver reads the secrets and merges them over the headers; its error names the source and the header
	headers, err := sync.ResolveHeaders(sync.SourceConfig{
		URI: settings.Source.URI, Headers: parsed.headers, HeadersFrom: parsed.headersFrom,
	})
	if err != nil {
		return nil, fmt.Errorf("the %s fetch trigger: %w", Type, err)
	}
	if _, err := checkHeaders(headers); err != nil {
		return nil, fmt.Errorf("the %s fetch trigger of %q: %w", Type, settings.Source.URI, err)
	}
	return &trigger{
		url:     parsed.target.String(),
		shown:   describe(parsed.target),
		headers: headers,
		idle:    parsed.idle,
		log:     settings.Logger,
		client:  newClient(),
	}, nil
}

// the settings of a trigger, read and checked
type parsedSettings struct {
	target      *url.URL
	headers     map[string]string
	headersFrom map[string]secret.Ref
	idle        time.Duration
}

// allowedSettings lists the settings for the error of an unknown one.
func allowedSettings() string {
	names := []string{settingURL, settingHeaders, settingHeadersFrom, settingIdleTimeout}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// parseSettings reads and checks the settings. An error names the setting and, for a header, its name: never a value.
func parseSettings(config map[string]any) (parsedSettings, error) {
	var unknown []string
	for key := range config {
		switch key {
		case settingURL, settingHeaders, settingHeadersFrom, settingIdleTimeout:
		default:
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return parsedSettings{}, fmt.Errorf("unknown setting %q (allowed: %s)", unknown[0], allowedSettings())
	}
	target, err := parseURL(config)
	if err != nil {
		return parsedSettings{}, err
	}
	headers, err := parseHeaders(config[settingHeaders])
	if err != nil {
		return parsedSettings{}, err
	}
	from, err := parseHeadersFrom(config[settingHeadersFrom])
	if err != nil {
		return parsedSettings{}, err
	}
	idle, err := parseIdleTimeout(config[settingIdleTimeout])
	if err != nil {
		return parsedSettings{}, err
	}
	return parsedSettings{target: target, headers: headers, headersFrom: from, idle: idle}, nil
}

func parseURL(config map[string]any) (*url.URL, error) {
	raw, ok := config[settingURL]
	if !ok {
		return nil, fmt.Errorf("the setting %q is mandatory: the address of the event stream", settingURL)
	}
	text, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("the setting %q must be text", settingURL)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("the setting %q is mandatory: the address of the event stream", settingURL)
	}
	target, err := url.Parse(text)
	if err != nil {
		// the parse error quotes the address, which may carry a token in its query
		return nil, fmt.Errorf("the setting %q is not a valid address", settingURL)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("the setting %q must be an http or https address", settingURL)
	}
	if target.Hostname() == "" {
		return nil, fmt.Errorf("the setting %q needs a host", settingURL)
	}

	return target, nil
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

func parseHeadersFrom(raw any) (map[string]secret.Ref, error) {
	var out map[string]secret.Ref
	set := func(name string, ref secret.Ref) {
		if out == nil {
			out = map[string]secret.Ref{}
		}
		out[name] = ref
	}
	switch h := raw.(type) {
	case nil:
		return nil, nil
	case map[string]secret.Ref:
		for name, ref := range h {
			set(name, ref)
		}
	case map[string]map[string]string:
		for name, m := range h {
			ref, err := refFromStrings(name, m)
			if err != nil {
				return nil, err
			}
			set(name, ref)
		}
	case map[string]any:
		for name, v := range h {
			var ref secret.Ref
			switch entry := v.(type) {
			case secret.Ref:
				ref = entry
			case map[string]string:
				var err error
				if ref, err = refFromStrings(name, entry); err != nil {
					return nil, err
				}
			case map[string]any:
				strs := make(map[string]string, len(entry))
				for key, value := range entry {
					text, ok := value.(string)
					if !ok && (key == "env" || key == "file") {
						return nil, fmt.Errorf("the header %q in %q: %q must be text", name, settingHeadersFrom, key)
					}
					strs[key] = text
				}
				var err error
				if ref, err = refFromStrings(name, strs); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("the header %q in %q must be a mapping with env or file", name, settingHeadersFrom)
			}
			set(name, ref)
		}
	default:
		return nil, fmt.Errorf("the setting %q must be a mapping of header names to {\"env\": NAME} or {\"file\": PATH}", settingHeadersFrom)
	}
	return out, nil
}

// refFromStrings reads {"env": NAME} or {"file": PATH}; which of the two a secret names is checked when it is read.
func refFromStrings(header string, m map[string]string) (secret.Ref, error) {
	var ref secret.Ref
	for key, value := range m {
		switch key {
		case "env":
			ref.Env = value
		case "file":
			ref.File = value
		default:
			return secret.Ref{}, fmt.Errorf("the header %q in %q: unknown key %q (allowed: env, file)", header, settingHeadersFrom, key)
		}
	}
	return ref, nil
}

func parseIdleTimeout(raw any) (time.Duration, error) {
	var d time.Duration
	switch v := raw.(type) {
	case nil:
		return 0, nil
	case time.Duration:
		d = v
	case string:
		parsed, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil {
			return 0, fmt.Errorf("the setting %q must be a duration such as \"5m\"", settingIdleTimeout)
		}
		d = parsed
	default:
		return 0, fmt.Errorf("the setting %q must be a duration such as \"5m\"", settingIdleTimeout)
	}
	if d <= 0 {
		return 0, fmt.Errorf("the setting %q must be a duration such as \"5m\", longer than zero", settingIdleTimeout)
	}
	return d, nil
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
	// idle is how long the stream may send no byte before Run ends; zero means no limit.
	idle   time.Duration
	log    *logger.Logger
	client *http.Client
}

var _ sync.Trigger = (*trigger)(nil)

// Run connects to the stream and signals once for each event it dispatches, until ctx is done, when it returns nil. A
// stream that cannot be reached, answers anything but an event stream, breaks or is closed by the server is an error,
// so that the fetching sync provider runs Run again after a backoff.
func (t *trigger) Run(ctx context.Context, signal func()) error {
	defer t.client.CloseIdleConnections()

	// reqCtx ends the request on shutdown, and when the stream has been silent for too long
	reqCtx, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, t.url, nil)
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

	var body io.Reader = resp.Body
	var silence *idleReader
	if t.idle > 0 {
		silence = newIdleReader(resp.Body, t.idle, cancelRequest)
		defer silence.stop()
		body = silence
	}
	err = readEvents(body, func(event) { signal() })
	if ctx.Err() != nil {
		return nil
	}
	if silence != nil && silence.expired() {
		return fmt.Errorf("the event stream at %s sent no data for %s", t.shown, t.idle)
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

// idleReader reads from r and calls onIdle when no byte has arrived for d, which ends a read that would block.
type idleReader struct {
	r      io.Reader
	d      time.Duration
	timer  *time.Timer
	mu     gosync.Mutex
	fired  bool
	closed bool
}

func newIdleReader(r io.Reader, d time.Duration, onIdle func()) *idleReader {
	i := &idleReader{r: r, d: d}
	i.timer = time.AfterFunc(d, func() {
		i.mu.Lock()
		i.fired = !i.closed
		i.mu.Unlock()
		onIdle()
	})
	return i
}

func (i *idleReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.timer.Reset(i.d)
	}
	return n, err
}

// expired reports whether the timer ended the stream.
func (i *idleReader) expired() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.fired
}

func (i *idleReader) stop() {
	i.mu.Lock()
	i.closed = true
	i.mu.Unlock()
	i.timer.Stop()
}
