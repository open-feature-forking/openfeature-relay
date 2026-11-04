// Package webhook is the official webhook event sink: it posts batches of events as JSON to a URL. It imports only the
// core (no vendor client, transport library or other relay module).
//
// A distribution selects it with the settings
//
//	{"url": "https://hooks.example.com/flagd", "timeout": "5s", "headers": {"Authorization": "Bearer <token>"}}
//
// The headers are sent with every request, which is how a credential reaches the receiver. A header value is never
// logged and never appears in an error.
//
// By default the sink subscribes to every kind of event. Created with only configuration change events as its
// kinds, it works as a notifier that tells another system about flag changes.
//
// The webhook reports a server error, a throttling answer and a connection failure as failures the core repeats,
// and any other refusal as permanent, which the core drops and counts without repeating.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
)

// Type is the type name of the event sink.
const Type = "webhook"

// The settings of the sink.
const (
	SettingURL     = "url"
	SettingTimeout = "timeout"
	SettingHeaders = "headers"
)

const defaultTimeout = 5 * time.Second

// Factory creates the webhook event sink.
type Factory struct {
	// EventKinds are the kinds the sink subscribes to; nil means every kind.
	EventKinds []events.Kind
	// Client sends the requests; nil means a client without a timeout of its own (the sink bounds each request).
	Client *http.Client
}

var _ eventsink.Factory = Factory{}

// Type returns "webhook".
func (Factory) Type() string { return Type }

// Kinds returns the kinds the sink subscribes to.
func (f Factory) Kinds() []events.Kind {
	if f.EventKinds != nil {
		return f.EventKinds
	}
	return []events.Kind{events.KindConfigurationChange, events.KindEvaluation, events.KindTracking}
}

// Create builds the sink from the settings "url" and, optionally, "timeout". A faulty value is an error that names it.
func (f Factory) Create(s eventsink.Settings) (eventsink.Sink, error) {
	raw, ok := s.Config[SettingURL].(string)
	if !ok || raw == "" {
		return nil, fmt.Errorf("setting %q is required and must be a URL", SettingURL)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("setting %q: %q is not an http or https URL", SettingURL, raw)
	}
	timeout := defaultTimeout
	switch v := s.Config[SettingTimeout].(type) {
	case nil:
	case string:
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("setting %q: %q is not a positive duration", SettingTimeout, v)
		}
		timeout = d
	case time.Duration:
		if v <= 0 {
			return nil, fmt.Errorf("setting %q: %v is not a positive duration", SettingTimeout, v)
		}
		timeout = v
	default:
		return nil, fmt.Errorf("setting %q: %v is not a duration", SettingTimeout, v)
	}
	client := f.Client
	if client == nil {
		client = &http.Client{}
	}
	headers, err := parseHeaders(s.Config[SettingHeaders])
	if err != nil {
		return nil, err
	}
	return &Webhook{url: u.String(), timeout: timeout, client: client, headers: headers}, nil
}

// parseHeaders reads the mapping of header names to values. An error names the setting and the header, never a value.
func parseHeaders(raw any) (http.Header, error) {
	if raw == nil {
		return nil, nil
	}
	values := map[string]string{}
	switch m := raw.(type) {
	case map[string]string:
		values = m
	case map[string]any:
		for name, v := range m {
			str, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("setting %q: the value of header %q is not a string", SettingHeaders, name)
			}
			values[name] = str
		}
	default:
		return nil, fmt.Errorf("setting %q: expected a mapping of header names to strings, observed %T", SettingHeaders, raw)
	}
	h := make(http.Header, len(values))
	for name, value := range values {
		if !validHeaderName(name) {
			return nil, fmt.Errorf("setting %q: %q is not a valid header name", SettingHeaders, name)
		}
		switch http.CanonicalHeaderKey(name) {
		case "Content-Type", "Content-Length", "Host":
			return nil, fmt.Errorf("setting %q: the sink sets the header %q itself", SettingHeaders, name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("setting %q: the value of header %q contains a line break", SettingHeaders, name)
		}
		h.Set(name, value)
	}
	return h, nil
}

// validHeaderName reports whether name is an RFC 9110 token.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.ContainsRune("!#$%&'*+-.^_`|~", c):
		default:
			return false
		}
	}
	return true
}

// Webhook posts batches of events to a URL.
type Webhook struct {
	url     string
	timeout time.Duration
	client  *http.Client
	headers http.Header
}

// Deliver posts the batch as a JSON array.
func (w *Webhook) Deliver(ctx context.Context, batch []events.Event) error {
	body, err := json.Marshal(payloads(batch))
	if err != nil {
		return eventsink.Permanent(fmt.Errorf("encoding the batch: %w", err))
	}
	ctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return eventsink.Permanent(err)
	}
	for name, values := range w.headers {
		req.Header[name] = values
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("posting the batch: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("the webhook answered %s", resp.Status)
	}
	return eventsink.Permanent(fmt.Errorf("the webhook refused the batch with %s", resp.Status))
}

// payloads turns events into the JSON the webhook receives: an object per event, with its kind.
func payloads(batch []events.Event) []map[string]any {
	out := make([]map[string]any, 0, len(batch))
	for _, e := range batch {
		switch v := e.(type) {
		case events.ConfigurationChange:
			out = append(out, map[string]any{
				"kind": events.KindConfigurationChange, "time": v.Time, "source": v.Source, "format": v.Format,
				"flagSet": v.FlagSet, "version": v.Version, "added": v.Added, "updated": v.Updated, "removed": v.Removed,
				"missed": v.Missed,
			})
		case events.Evaluation:
			out = append(out, map[string]any{
				"kind": events.KindEvaluation, "time": v.Time, "flagKey": v.FlagKey, "flagSet": v.FlagSet,
				"variant": v.Variant, "reason": v.Reason, "errorCode": v.ErrorCode, "targetingKey": v.TargetingKey,
				"value": v.Value, "hasValue": v.HasValue, "origin": v.Origin,
			})
		case events.Tracking:
			out = append(out, map[string]any{
				"kind": events.KindTracking, "time": v.Time, "name": v.EventName, "targetingKey": v.TargetingKey,
				"value": v.Value, "details": v.Details, "origin": v.Origin,
			})
		}
	}
	return out
}

// NewFactory returns the factory of the webhook event sink, as a generated main of a distribution calls it.
func NewFactory() eventsink.Factory { return Factory{} }
