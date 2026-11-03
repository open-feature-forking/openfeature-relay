package telemetry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coretel "github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry/telemetrytest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// gate blocks exports while the backend stalls.
type gate struct {
	mu sync.Mutex
	ch chan struct{}
}

func newGate() *gate {
	ch := make(chan struct{})
	close(ch)
	return &gate{ch: ch}
}

func (g *gate) stall() func() {
	g.mu.Lock()
	ch := make(chan struct{})
	g.ch = ch
	g.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

func (g *gate) wait(ctx context.Context) error {
	g.mu.Lock()
	ch := g.ch
	g.mu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// backend is an in-memory span and metric exporter that can stall.
type backend struct {
	gate    *gate
	mu      sync.Mutex
	spans   []string
	metrics []string
}

func newBackend() *backend { return &backend{gate: newGate()} }

func (b *backend) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	if err := b.gate.wait(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range spans {
		b.spans = append(b.spans, s.Name())
	}
	return nil
}

func (b *backend) Temporality(k metric.InstrumentKind) metricdata.Temporality {
	return metric.DefaultTemporalitySelector(k)
}

func (b *backend) Aggregation(k metric.InstrumentKind) metric.Aggregation {
	return metric.DefaultAggregationSelector(k)
}

func (b *backend) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	if err := b.gate.wait(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			b.metrics = append(b.metrics, m.Name)
		}
	}
	return nil
}

func (b *backend) ForceFlush(context.Context) error { return nil }

func (b *backend) Shutdown(context.Context) error { return nil }

func (b *backend) observed(testing.TB) telemetrytest.Observation {
	b.mu.Lock()
	defer b.mu.Unlock()
	return telemetrytest.Observation{Spans: slices.Clone(b.spans), Metrics: slices.Clone(b.metrics)}
}

// contractFactory gives each created provider a fresh backend; the test inputs act on the latest one.
type contractFactory struct {
	mu     sync.Mutex
	latest *backend
}

func (f *contractFactory) Type() string { return Type }

func (f *contractFactory) Create(ctx context.Context, s coretel.Settings) (coretel.Provider, error) {
	b := newBackend()
	p, err := NewFactory(
		WithoutGlobals(),
		WithSpanExporter(b),
		WithMetricReader(metric.NewPeriodicReader(b)),
	).Create(ctx, s)
	if err == nil {
		f.mu.Lock()
		f.latest = b
		f.mu.Unlock()
	}
	return p, err
}

func (f *contractFactory) current() *backend {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latest
}

func TestContract(t *testing.T) {
	f := &contractFactory{}
	telemetrytest.Run(t, f, telemetrytest.Inputs{
		Config:        map[string]any{SettingMetricsExporter: "", SettingReloadInterval: "1h"},
		InvalidConfig: map[string]any{SettingReloadInterval: "soon"},
		FaultyValue:   "soon",
		StallBackend:  func(testing.TB) func() { return f.current().gate.stall() },
		Observed:      func(t testing.TB) telemetrytest.Observation { return f.current().observed(t) },
	})
}

func settings(config map[string]any) coretel.Settings {
	return coretel.Settings{
		Logger:              logger.NewLogger(nil, false),
		DistributionName:    "flagd",
		DistributionVersion: "test",
		Config:              config,
	}
}

func TestInvalidSettingsAreNamed(t *testing.T) {
	tests := map[string]struct {
		config map[string]any
		want   []string
	}{
		"unknown key":     {map[string]any{"collectorUri": "x"}, []string{`"collectorUri"`}},
		"wrong type":      {map[string]any{SettingCollectorURI: 4317}, []string{`"collectorURI"`, "4317"}},
		"bad duration":    {map[string]any{SettingReloadInterval: "soon"}, []string{`"reloadInterval"`, `"soon"`}},
		"duration's type": {map[string]any{SettingReloadInterval: 3}, []string{`"reloadInterval"`, "3"}},
	}
	for name, tt := range tests {
		_, err := NewFactory(WithoutGlobals(), WithMetricReader(metric.NewManualReader())).
			Create(context.Background(), settings(tt.config))
		require.Error(t, err, name)
		for _, w := range tt.want {
			require.Contains(t, err.Error(), w, name)
		}
		require.Contains(t, err.Error(), `"otel"`, name)
	}
}

func TestUnbuildableSpanExporterIsAnError(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "foo")
	_, err := NewFactory(WithoutGlobals(), WithMetricReader(metric.NewManualReader())).
		Create(context.Background(), settings(nil))
	require.ErrorContains(t, err, "foo")
}

func TestWithoutGlobalsLeavesGlobalStateAlone(t *testing.T) {
	before := otel.GetMeterProvider()
	beforeTracer := otel.GetTracerProvider()
	p, err := NewFactory(WithoutGlobals(), WithSpanExporter(tracetest.NewInMemoryExporter()),
		WithMetricReader(metric.NewManualReader())).Create(context.Background(), settings(nil))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	require.Equal(t, before, otel.GetMeterProvider())
	require.Equal(t, beforeTracer, otel.GetTracerProvider())
}

func newProvider(t *testing.T, spans *tracetest.InMemoryExporter, reader metric.Reader) *Provider {
	t.Helper()
	opts := []Option{WithoutGlobals(), WithMetricReader(reader)}
	if spans != nil {
		opts = append(opts, WithSpanExporter(spans))
	}
	p, err := NewFactory(opts...).Create(context.Background(), settings(nil))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p.(*Provider)
}

func TestSpans(t *testing.T) {
	spans := tracetest.NewInMemoryExporter()
	p := newProvider(t, spans, metric.NewManualReader())

	ctx, parent := p.Tracer("flagEvaluationService").Start(context.Background(), "resolveBoolean",
		coretel.WithSpanKind(coretel.SpanKindServer), coretel.WithAttributes(coretel.String("feature_flag.source", "s")))
	_, child := p.Tracer("jsonEvaluator").Start(ctx, "resolveBoolean")
	child.End()
	parent.SetAttributes(coretel.Int("feature_flag.count", 2), coretel.Bool("b", true), coretel.Float64("f", 0.5))
	parent.RecordError(errors.New("boom"))
	parent.SetError("error evaluating flag with key k")
	parent.End()
	// the in-memory exporter forgets its spans on shutdown
	require.NoError(t, p.sdkTracerProvider.ForceFlush(context.Background()))

	got := spans.GetSpans()
	require.Len(t, got, 2)
	c, s := got[0], got[1]
	require.Equal(t, "jsonEvaluator", c.InstrumentationScope.Name)
	require.Equal(t, oteltrace.SpanKindInternal, c.SpanKind)
	require.Equal(t, s.SpanContext.SpanID(), c.Parent.SpanID())
	require.Equal(t, "flagEvaluationService", s.InstrumentationScope.Name)
	require.Equal(t, oteltrace.SpanKindServer, s.SpanKind)
	require.Equal(t, codes.Error, s.Status.Code)
	require.Equal(t, "error evaluating flag with key k", s.Status.Description)
	require.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("feature_flag.source", "s"),
		attribute.Int64("feature_flag.count", 2),
		attribute.Bool("b", true),
		attribute.Float64("f", 0.5),
	}, s.Attributes)
	require.Len(t, s.Events, 1)
	require.Equal(t, "exception", s.Events[0].Name)
}

const traceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"

// The incoming trace parents the server span only when a span exporter is configured, as before.
func TestHTTPServerSpansPropagation(t *testing.T) {
	serve := func(p *Provider) {
		h := p.HTTPServerSpans(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "flagd.ofrep")
		req := httptest.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags", nil)
		req.Header.Set("traceparent", traceparent)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	spans := tracetest.NewInMemoryExporter()
	withExporter := newProvider(t, spans, metric.NewManualReader())
	serve(withExporter)
	require.NoError(t, withExporter.sdkTracerProvider.ForceFlush(context.Background()))
	got := spans.GetSpans()
	require.Len(t, got, 1)
	// otelhttp names the server span after the method, as TestOfrepEvaluationSpans pins today
	require.Equal(t, "POST", got[0].Name)
	require.Equal(t, "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp", got[0].InstrumentationScope.Name)
	require.Equal(t, oteltrace.SpanKindServer, got[0].SpanKind)
	require.Equal(t, "0af7651916cd43dd8448eb211c80319c", got[0].SpanContext.TraceID().String())

	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	without := newProvider(t, nil, metric.NewManualReader())
	require.Nil(t, without.sdkTracerProvider)
	serve(without) // a noop tracer: nothing to observe, and no panic
}

func TestHTTPServerSpansRecordNoMetrics(t *testing.T) {
	reader := metric.NewManualReader()
	p := newProvider(t, nil, reader)
	h := p.HTTPServerSpans(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "op")
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	for _, sm := range rm.ScopeMetrics {
		require.Empty(t, sm.Metrics, sm.Scope.Name)
	}
}

func collectByName(t *testing.T, reader metric.Reader) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	out := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

func histogramAttrs(t *testing.T, m metricdata.Metrics) []attribute.Set {
	t.Helper()
	h, ok := m.Data.(metricdata.Histogram[float64])
	require.True(t, ok, m.Name)
	var sets []attribute.Set
	for _, dp := range h.DataPoints {
		sets = append(sets, dp.Attributes)
	}
	return sets
}

func TestHTTPServerMetrics(t *testing.T) {
	reader := metric.NewManualReader()
	p := newProvider(t, nil, reader)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("hello"))
	})

	fixed := p.HTTPServerMetrics(inner, coretel.HTTPMetricsOptions{Service: "flagd", Route: "/ofrep/v1/evaluate/flags"})
	byRequest := p.HTTPServerMetrics(inner, coretel.HTTPMetricsOptions{
		Service:          "flagd",
		RouteFromRequest: func(*http.Request) string { return "/flagd.evaluation.v1.Service/ResolveBoolean" },
	})
	byPath := p.HTTPServerMetrics(inner, coretel.HTTPMetricsOptions{Service: "flagd"})

	fixed.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags", nil))
	byRequest.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/anything", nil))
	byPath.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("BREW", "/missing", nil))

	metrics := collectByName(t, reader)
	duration := metrics[httpRequestDurationMetric]
	require.Equal(t, "s", duration.Unit)
	var routes []string
	for _, set := range histogramAttrs(t, duration) {
		route, _ := set.Value("http.route")
		method, _ := set.Value("http.request.method")
		code, _ := set.Value("http.response.status_code")
		service, _ := set.Value("service.name")
		require.Equal(t, "flagd", service.AsString())
		routes = append(routes, route.AsString()+" "+method.AsString()+" "+code.AsString())
	}
	require.ElementsMatch(t, []string{
		"/ofrep/v1/evaluate/flags POST 200",
		"/flagd.evaluation.v1.Service/ResolveBoolean POST 200",
		"/missing _OTHER 404",
	}, routes)

	size := metrics[httpResponseSizeMetric].Data.(metricdata.Histogram[float64])
	var bytes float64
	for _, dp := range size.DataPoints {
		bytes += dp.Sum
	}
	require.Equal(t, float64(2*len("hello")), bytes)

	active := metrics[httpActiveRequestsMetric].Data.(metricdata.Sum[int64])
	for _, dp := range active.DataPoints {
		require.Zero(t, dp.Value, "active requests after every request ended")
	}
}

func TestHTTPServerMetricsKeepsFlusher(t *testing.T) {
	p := newProvider(t, nil, metric.NewManualReader())
	flushed := false
	h := p.HTTPServerMetrics(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		require.True(t, ok)
		f.Flush()
		flushed = true
	}), coretel.HTTPMetricsOptions{})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	require.True(t, flushed)
}

func TestTransportInstrumentation(t *testing.T) {
	p := newProvider(t, nil, metric.NewManualReader())
	require.Len(t, p.ConnectHandlerOptions(), 1)
	require.Len(t, p.GRPCDialOptions(), 1)
	rec := httptest.NewRecorder()
	p.MetricsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, strings.Contains(rec.Body.String(), "go_goroutines"))
}

func TestConvert(t *testing.T) {
	require.Nil(t, convert(nil))
	require.Equal(t, []attribute.KeyValue{
		attribute.String("s", "v"),
		attribute.Int64("i", -3),
		attribute.Float64("f", 1.5),
		attribute.Bool("b", true),
		attribute.String("zero", ""),
	}, convert([]coretel.Attribute{
		coretel.String("s", "v"),
		coretel.Int64("i", -3),
		coretel.Float64("f", 1.5),
		coretel.Bool("b", true),
		{Key: "zero"},
	}))
}
