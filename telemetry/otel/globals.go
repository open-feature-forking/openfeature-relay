package telemetry

import (
	"go.opentelemetry.io/otel"
)

// FromGlobals returns a provider over the global tracer provider, meter provider and propagator that
// BuildTraceProvider and BuildMetricsRecorder install, for a runtime that still installs them itself. Its
// Shutdown flushes nothing.
func FromGlobals(serviceName string) *Provider {
	mp := otel.GetMeterProvider()
	return &Provider{
		serviceName:    serviceName,
		tracerProvider: otel.GetTracerProvider(),
		propagator:     otel.GetTextMapPropagator(),
		meterProvider:  mp,
		recorder:       newRecorder(mp.Meter(serviceName)),
	}
}
