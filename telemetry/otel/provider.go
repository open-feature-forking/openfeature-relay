package telemetry

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	coretel "github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	msdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

// Provider is the OpenTelemetry telemetry provider.
type Provider struct {
	serviceName string
	// sdkTracerProvider is nil when no span exporter is configured; tracerProvider is then a noop one.
	sdkTracerProvider *trace.TracerProvider
	tracerProvider    oteltrace.TracerProvider
	propagator        propagation.TextMapPropagator
	meterProvider     metric.MeterProvider
	// sdkMeterProvider is nil for a provider over the global state; see FromGlobals.
	sdkMeterProvider *msdk.MeterProvider
	recorder         *MetricsRecorder
}

var _ coretel.Provider = (*Provider)(nil)

// installGlobals sets the global state otelconnect, otelgrpc and third-party instrumentation read, as flagd did
// before the telemetry provider existed: the meter provider always, the tracer provider and the propagator only
// with a span exporter.
func (p *Provider) installGlobals() {
	otel.SetMeterProvider(p.sdkMeterProvider)
	if p.sdkTracerProvider != nil {
		otel.SetTracerProvider(p.sdkTracerProvider)
		otel.SetTextMapPropagator(p.propagator)
	}
}

// Tracer returns the tracer of an instrumentation scope.
func (p *Provider) Tracer(scope string) coretel.Tracer {
	return tracer{p.tracerProvider.Tracer(scope)}
}

// Meter returns the meter of an instrumentation scope.
func (p *Provider) Meter(scope string) coretel.Meter {
	return meter{p.meterProvider.Meter(scope)}
}

// HTTPServerSpans records each request as a server span; its metrics are left to HTTPServerMetrics.
func (p *Provider) HTTPServerSpans(h http.Handler, operation string) http.Handler {
	return otelhttp.NewHandler(h, operation,
		otelhttp.WithTracerProvider(p.tracerProvider),
		otelhttp.WithPropagators(p.propagator),
		otelhttp.WithMeterProvider(metricnoop.NewMeterProvider()))
}

// HTTPServerMetrics records flagd's HTTP server metrics: request duration, response size and active requests.
func (p *Provider) HTTPServerMetrics(h http.Handler, opts coretel.HTTPMetricsOptions) http.Handler {
	return newHTTPMetrics(p.recorder, opts).handler(h)
}

// Shutdown flushes the spans and metrics the provider holds and stops its exporters.
func (p *Provider) Shutdown(ctx context.Context) error {
	var errs []error
	if p.sdkTracerProvider != nil {
		errs = append(errs, p.sdkTracerProvider.Shutdown(ctx))
	}
	if p.sdkMeterProvider != nil {
		errs = append(errs, p.sdkMeterProvider.Shutdown(ctx))
	}
	return errors.Join(errs...)
}

// ForceFlush exports the spans the provider still holds.
func (p *Provider) ForceFlush(ctx context.Context) error {
	if p.sdkTracerProvider == nil {
		return nil
	}
	return p.sdkTracerProvider.ForceFlush(ctx)
}

// ConnectHandlerOptions instrument a ConnectRPC handler with otelconnect, as flagd's flag evaluation and flag sync
// endpoints always have.
func (p *Provider) ConnectHandlerOptions() []connect.HandlerOption {
	interceptor, err := otelconnect.NewInterceptor(
		otelconnect.WithTrustRemote(),
		// this option prevents a cardinality explosion in OTel due to remote peer and ephemeral port attributes
		otelconnect.WithoutServerPeerAttributes(),
		otelconnect.WithTracerProvider(p.tracerProvider),
		otelconnect.WithMeterProvider(p.meterProvider),
		otelconnect.WithPropagator(p.propagator),
	)
	if err != nil {
		otel.Handle(err)
		return nil
	}
	return []connect.HandlerOption{connect.WithInterceptors(interceptor)}
}

// GRPCDialOptions instrument an outbound gRPC client connection, such as a flagd-grpc sync source, so that the
// rpc.client.* metrics and spans land on this provider.
func (p *Provider) GRPCDialOptions() []grpc.DialOption {
	return []grpc.DialOption{grpc.WithStatsHandler(otelgrpc.NewClientHandler(
		otelgrpc.WithTracerProvider(p.tracerProvider),
		otelgrpc.WithMeterProvider(p.meterProvider),
		otelgrpc.WithPropagators(p.propagator),
	))}
}

// MetricsHandler serves the Prometheus metrics on the management port.
func (p *Provider) MetricsHandler() http.Handler {
	return promhttp.Handler()
}

type tracer struct{ t oteltrace.Tracer }

func (t tracer) Start(ctx context.Context, name string, opts ...coretel.SpanOption) (context.Context, coretel.Span) {
	cfg := coretel.NewSpanConfig(opts...)
	if len(cfg.Attributes) == 0 {
		ctx, s := t.t.Start(ctx, name, oteltrace.WithSpanKind(spanKind(cfg.Kind)))
		return ctx, span{s}
	}
	kvs := converted(cfg.Attributes)
	ctx, s := t.t.Start(ctx, name, oteltrace.WithSpanKind(spanKind(cfg.Kind)), oteltrace.WithAttributes(*kvs...))
	release(kvs)
	return ctx, span{s}
}

func spanKind(k coretel.SpanKind) oteltrace.SpanKind {
	switch k {
	case coretel.SpanKindServer:
		return oteltrace.SpanKindServer
	case coretel.SpanKindClient:
		return oteltrace.SpanKindClient
	default:
		return oteltrace.SpanKindInternal
	}
}

type span struct{ s oteltrace.Span }

func (s span) SetAttributes(attrs ...coretel.Attribute) {
	kvs := converted(attrs)
	s.s.SetAttributes(*kvs...)
	release(kvs)
}

func (s span) RecordError(err error) { s.s.RecordError(err) }

func (s span) SetError(description string) { s.s.SetStatus(codes.Error, description) }

func (s span) End() { s.s.End() }

type meter struct{ m metric.Meter }

func instrumentOptions(opts []coretel.InstrumentOption) (description, unit string, buckets []float64) {
	c := coretel.NewInstrumentConfig(opts...)
	return c.Description, c.Unit, c.Buckets
}

func (m meter) Int64Counter(name string, opts ...coretel.InstrumentOption) coretel.Int64Counter {
	description, unit, _ := instrumentOptions(opts)
	c, err := m.m.Int64Counter(name, metric.WithDescription(description), metric.WithUnit(unit))
	if err != nil {
		otel.Handle(err)
	}
	return int64Counter{c}
}

func (m meter) Int64UpDownCounter(name string, opts ...coretel.InstrumentOption) coretel.Int64UpDownCounter {
	description, unit, _ := instrumentOptions(opts)
	c, err := m.m.Int64UpDownCounter(name, metric.WithDescription(description), metric.WithUnit(unit))
	if err != nil {
		otel.Handle(err)
	}
	return int64UpDownCounter{c}
}

func (m meter) Float64Histogram(name string, opts ...coretel.InstrumentOption) coretel.Float64Histogram {
	description, unit, buckets := instrumentOptions(opts)
	histogramOpts := []metric.Float64HistogramOption{metric.WithDescription(description), metric.WithUnit(unit)}
	if buckets != nil {
		histogramOpts = append(histogramOpts, metric.WithExplicitBucketBoundaries(buckets...))
	}
	h, err := m.m.Float64Histogram(name, histogramOpts...)
	if err != nil {
		otel.Handle(err)
	}
	return float64Histogram{h}
}

func (m meter) Int64Gauge(name string, opts ...coretel.InstrumentOption) coretel.Int64Gauge {
	description, unit, _ := instrumentOptions(opts)
	g, err := m.m.Int64Gauge(name, metric.WithDescription(description), metric.WithUnit(unit))
	if err != nil {
		otel.Handle(err)
	}
	return int64Gauge{g}
}

// The SDK returns a usable instrument together with an error, so the instruments below are never nil.

type int64Counter struct{ c metric.Int64Counter }

func (c int64Counter) Add(ctx context.Context, incr int64, attrs ...coretel.Attribute) {
	kvs := converted(attrs)
	c.c.Add(ctx, incr, metric.WithAttributes(*kvs...))
	release(kvs)
}

type int64Gauge struct{ g metric.Int64Gauge }

func (g int64Gauge) Record(ctx context.Context, value int64, attrs ...coretel.Attribute) {
	kvs := converted(attrs)
	g.g.Record(ctx, value, metric.WithAttributes(*kvs...))
	release(kvs)
}

type int64UpDownCounter struct{ c metric.Int64UpDownCounter }

func (c int64UpDownCounter) Add(ctx context.Context, incr int64, attrs ...coretel.Attribute) {
	kvs := converted(attrs)
	c.c.Add(ctx, incr, metric.WithAttributes(*kvs...))
	release(kvs)
}

type float64Histogram struct{ h metric.Float64Histogram }

func (h float64Histogram) Record(ctx context.Context, value float64, attrs ...coretel.Attribute) {
	kvs := converted(attrs)
	h.h.Record(ctx, value, metric.WithAttributes(*kvs...))
	release(kvs)
}

// kvPool holds the buffers of converted attributes. The SDK copies the attributes of a measurement and of a span,
// so a buffer is reused once the call that received it returns.
var kvPool = sync.Pool{New: func() any {
	kvs := make([]attribute.KeyValue, 0, 8)
	return &kvs
}}

// converted returns attrs as OpenTelemetry attributes in a pooled buffer; release returns the buffer.
func converted(attrs []coretel.Attribute) *[]attribute.KeyValue {
	kvs := kvPool.Get().(*[]attribute.KeyValue)
	*kvs = convertInto((*kvs)[:0], attrs)
	return kvs
}

func release(kvs *[]attribute.KeyValue) {
	clear(*kvs)
	*kvs = (*kvs)[:0]
	kvPool.Put(kvs)
}

// convert turns core attributes into OpenTelemetry attributes; an Invalid value becomes an empty string.
func convert(attrs []coretel.Attribute) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	return convertInto(make([]attribute.KeyValue, 0, len(attrs)), attrs)
}

// convertInto appends attrs, as OpenTelemetry attributes, to dst.
func convertInto(dst []attribute.KeyValue, attrs []coretel.Attribute) []attribute.KeyValue {
	for _, a := range attrs {
		key := attribute.Key(a.Key)
		switch a.Value.Type() {
		case coretel.Int64Type:
			dst = append(dst, key.Int64(a.Value.AsInt64()))
		case coretel.Float64Type:
			dst = append(dst, key.Float64(a.Value.AsFloat64()))
		case coretel.BoolType:
			dst = append(dst, key.Bool(a.Value.AsBool()))
		default:
			dst = append(dst, key.String(a.Value.AsString()))
		}
	}
	return dst
}
