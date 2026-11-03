package telemetry

import (
	"context"
	"net/http"
)

// NoopType is the type name of the noop telemetry provider.
const NoopType = "noop"

// Noop returns the noop telemetry provider: it records nothing and leaves handlers unchanged.
func Noop() Provider { return noopProvider{} }

// OrNoop returns p, or the noop telemetry provider when p is nil.
func OrNoop(p Provider) Provider {
	if p == nil {
		return Noop()
	}
	return p
}

// NoopFactory returns the factory of the noop telemetry provider. It accepts any configuration.
func NoopFactory() Factory { return noopFactory{} }

type noopFactory struct{}

func (noopFactory) Type() string { return NoopType }

func (noopFactory) Create(context.Context, Settings) (Provider, error) { return Noop(), nil }

type noopProvider struct{}

func (noopProvider) Tracer(string) Tracer { return noopTracer{} }

func (noopProvider) Meter(string) Meter { return noopMeter{} }

func (noopProvider) HTTPServerSpans(h http.Handler, _ string) http.Handler { return h }

func (noopProvider) HTTPServerMetrics(h http.Handler, _ HTTPMetricsOptions) http.Handler { return h }

func (noopProvider) Shutdown(context.Context) error { return nil }

type noopTracer struct{}

func (noopTracer) Start(ctx context.Context, _ string, _ ...SpanOption) (context.Context, Span) {
	return ctx, noopSpan{}
}

type noopSpan struct{}

func (noopSpan) SetAttributes(...Attribute) {}

func (noopSpan) RecordError(error) {}

func (noopSpan) SetError(string) {}

func (noopSpan) End() {}

type noopMeter struct{}

func (noopMeter) Int64Counter(string, ...InstrumentOption) Int64Counter { return noopInstrument{} }

func (noopMeter) Int64UpDownCounter(string, ...InstrumentOption) Int64UpDownCounter {
	return noopInstrument{}
}

func (noopMeter) Float64Histogram(string, ...InstrumentOption) Float64Histogram {
	return noopInstrument{}
}

func (noopMeter) Int64Gauge(string, ...InstrumentOption) Int64Gauge { return noopGauge{} }

type noopGauge struct{}

func (noopGauge) Record(context.Context, int64, ...Attribute) {}

type noopInstrument struct{}

func (noopInstrument) Add(context.Context, int64, ...Attribute) {}

func (noopInstrument) Record(context.Context, float64, ...Attribute) {}
