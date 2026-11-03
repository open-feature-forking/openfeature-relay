// Package telemetryexample is an example telemetry provider extension: it keeps spans and metrics in memory and
// writes them as JSON lines to a file on shutdown. It shows what an Extension Author's component module contains,
// and imports only the core (no telemetry SDK, metrics client or transport).
//
// A distribution selects it as its telemetry provider with the setting
//
//	{"path": "/var/log/flagd-telemetry.jsonl"}
package telemetryexample

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// Type is the type name of the example telemetry provider.
const Type = "jsonlines"

// SettingPath is the setting that names the file the provider writes on shutdown.
const SettingPath = "path"

// Factory creates the example telemetry provider.
type Factory struct {
	// Open opens the file the provider writes; nil means os.Create.
	Open func(path string) (io.WriteCloser, error)
}

// Type returns "jsonlines".
func (Factory) Type() string { return Type }

// Create opens the file named by the path setting, so that a file that cannot be written stops startup instead of
// losing the telemetry on shutdown.
func (f Factory) Create(_ context.Context, s telemetry.Settings) (telemetry.Provider, error) {
	path := ""
	for key, value := range s.Config {
		if key != SettingPath {
			return nil, fmt.Errorf("telemetry provider %q: unknown setting %q", Type, key)
		}
		p, ok := value.(string)
		if !ok || p == "" {
			return nil, fmt.Errorf("telemetry provider %q: setting %q: %v is not a file path", Type, key, value)
		}
		path = p
	}
	if path == "" {
		return nil, fmt.Errorf("telemetry provider %q: setting %q is missing", Type, SettingPath)
	}
	open := f.Open
	if open == nil {
		open = func(path string) (io.WriteCloser, error) { return os.Create(path) }
	}
	w, err := open(path)
	if err != nil {
		return nil, fmt.Errorf("telemetry provider %q: setting %q: %w", Type, SettingPath, err)
	}
	return &Provider{out: w, metrics: map[string]*point{}}, nil
}

// Line is one line of the file: a span or a metric.
type Line struct {
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	Scope      string         `json:"scope"`
	Kind       string         `json:"kind,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
	// Spans only.
	Start time.Time `json:"start,omitzero"`
	End   time.Time `json:"end,omitzero"`
	Error string    `json:"error,omitempty"`
	// Metrics only: the sum of the measurements of one attribute set, and their number.
	Value float64 `json:"value,omitempty"`
	Count int64   `json:"count,omitempty"`
}

// Provider is the example telemetry provider.
type Provider struct {
	out io.WriteCloser

	mu      sync.Mutex
	spans   []Line
	metrics map[string]*point
	closed  bool
}

type point struct{ line Line }

func attributes(attrs []telemetry.Attribute) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	m := make(map[string]any, len(attrs))
	for _, a := range attrs {
		switch a.Value.Type() {
		case telemetry.Int64Type:
			m[a.Key] = a.Value.AsInt64()
		case telemetry.Float64Type:
			m[a.Key] = a.Value.AsFloat64()
		case telemetry.BoolType:
			m[a.Key] = a.Value.AsBool()
		default:
			m[a.Key] = a.Value.AsString()
		}
	}
	return m
}

func (p *Provider) endSpan(l Line) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.spans = append(p.spans, l)
	}
}

// measure adds a measurement to the point of its instrument and attribute set.
func (p *Provider) measure(scope, name, kind string, value float64, attrs []telemetry.Attribute) {
	keys := make([]string, 0, len(attrs))
	for _, a := range attrs {
		keys = append(keys, a.Key+"="+fmt.Sprint(attributes([]telemetry.Attribute{a})[a.Key]))
	}
	slices.Sort(keys)
	id := scope + "\x00" + name + "\x00" + strings.Join(keys, ",")

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	pt, ok := p.metrics[id]
	if !ok {
		pt = &point{line: Line{Type: "metric", Name: name, Scope: scope, Kind: kind, Attributes: attributes(attrs)}}
		p.metrics[id] = pt
	}
	pt.line.Value += value
	pt.line.Count++
}

// Tracer returns the tracer of an instrumentation scope.
func (p *Provider) Tracer(scope string) telemetry.Tracer { return tracer{p: p, scope: scope} }

// Meter returns the meter of an instrumentation scope.
func (p *Provider) Meter(scope string) telemetry.Meter { return meter{p: p, scope: scope} }

// statusRecorder keeps the status code a handler writes.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// HTTPServerSpans records each request as a server span named operation.
func (p *Provider) HTTPServerSpans(h http.Handler, operation string) http.Handler {
	t := p.Tracer("telemetryexample/http")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := t.Start(r.Context(), operation, telemetry.WithSpanKind(telemetry.SpanKindServer))
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r.WithContext(ctx))
		span.SetAttributes(
			telemetry.String("http.request.method", r.Method),
			telemetry.String("url.path", r.URL.Path),
			telemetry.Int("http.response.status_code", rec.status),
		)
		span.End()
	})
}

// HTTPServerMetrics records the duration of each request in the histogram http.server.request.duration.
func (p *Provider) HTTPServerMetrics(h http.Handler, opts telemetry.HTTPMetricsOptions) http.Handler {
	duration := p.Meter("telemetryexample/http").Float64Histogram("http.server.request.duration", telemetry.WithUnit("s"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r)
		route := opts.Route
		if opts.RouteFromRequest != nil {
			route = opts.RouteFromRequest(r)
		}
		if route == "" {
			route = r.URL.Path
		}
		duration.Record(r.Context(), time.Since(start).Seconds(),
			telemetry.String("service.name", opts.Service),
			telemetry.String("http.route", route),
			telemetry.String("http.request.method", r.Method),
			telemetry.String("http.response.status_code", strconv.Itoa(rec.status)),
		)
	})
}

// Shutdown writes the spans and metrics as JSON lines and closes the file. It returns ctx's error when the file
// cannot be written before ctx is done; the write then finishes in the background.
func (p *Provider) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	lines := p.spans
	for _, pt := range p.metrics {
		lines = append(lines, pt.line)
	}
	p.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		enc := json.NewEncoder(p.out)
		var err error
		for _, l := range lines {
			if err = enc.Encode(l); err != nil {
				break
			}
		}
		done <- errors.Join(err, p.out.Close())
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type tracer struct {
	p     *Provider
	scope string
}

func (t tracer) Start(ctx context.Context, name string, opts ...telemetry.SpanOption) (context.Context, telemetry.Span) {
	cfg := telemetry.NewSpanConfig(opts...)
	kind := "internal"
	switch cfg.Kind {
	case telemetry.SpanKindServer:
		kind = "server"
	case telemetry.SpanKindClient:
		kind = "client"
	}
	s := &span{p: t.p, line: Line{Type: "span", Name: name, Scope: t.scope, Kind: kind, Start: time.Now()}}
	s.SetAttributes(cfg.Attributes...)
	return ctx, s
}

type span struct {
	p    *Provider
	mu   sync.Mutex
	line Line
}

func (s *span) SetAttributes(attrs ...telemetry.Attribute) {
	if len(attrs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.line.Attributes == nil {
		s.line.Attributes = map[string]any{}
	}
	for k, v := range attributes(attrs) {
		s.line.Attributes[k] = v
	}
}

func (s *span) RecordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil && s.line.Error == "" {
		s.line.Error = err.Error()
	}
}

func (s *span) SetError(description string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.line.Error = description
}

func (s *span) End() {
	s.mu.Lock()
	l := s.line
	s.mu.Unlock()
	l.End = time.Now()
	s.p.endSpan(l)
}

type meter struct {
	p     *Provider
	scope string
}

func (m meter) Int64Counter(name string, _ ...telemetry.InstrumentOption) telemetry.Int64Counter {
	return instrument{p: m.p, scope: m.scope, name: name, kind: "counter"}
}

func (m meter) Int64UpDownCounter(name string, _ ...telemetry.InstrumentOption) telemetry.Int64UpDownCounter {
	return instrument{p: m.p, scope: m.scope, name: name, kind: "updowncounter"}
}

func (m meter) Float64Histogram(name string, _ ...telemetry.InstrumentOption) telemetry.Float64Histogram {
	return instrument{p: m.p, scope: m.scope, name: name, kind: "histogram"}
}

type instrument struct {
	p     *Provider
	scope string
	name  string
	kind  string
}

func (i instrument) Add(_ context.Context, incr int64, attrs ...telemetry.Attribute) {
	i.p.measure(i.scope, i.name, i.kind, float64(incr), attrs)
}

func (i instrument) Record(_ context.Context, value float64, attrs ...telemetry.Attribute) {
	i.p.measure(i.scope, i.name, i.kind, value, attrs)
}
