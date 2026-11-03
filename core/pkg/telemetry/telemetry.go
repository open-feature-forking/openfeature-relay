// Package telemetry defines the telemetry provider: the tracing, metrics and HTTP instrumentation that the runtime,
// sync providers, service endpoints, middleware and event sinks record through. The evaluator and configuration
// formats record nothing.
//
// A distribution injects one provider. Noop is used when it selects none.
//
// Methods that take attributes or options are variadic. Through an interface the argument slice escapes, so a call
// that passes attributes or options costs one allocation even with the noop provider; a call without them, and
// building an attribute, cost none.
package telemetry

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
)

// Provider supplies tracing, metrics and HTTP instrumentation. It is safe for concurrent use and never blocks a
// request on its backend.
type Provider interface {
	// Tracer returns the tracer of an instrumentation scope.
	Tracer(scope string) Tracer
	// Meter returns the meter of an instrumentation scope.
	Meter(scope string) Meter
	// HTTPServerSpans wraps h so that each request is recorded as a server span named operation, continuing a trace
	// propagated by the client.
	HTTPServerSpans(h http.Handler, operation string) http.Handler
	// HTTPServerMetrics wraps h so that each request is counted in the HTTP server metrics.
	HTTPServerMetrics(h http.Handler, opts HTTPMetricsOptions) http.Handler
	// Shutdown delivers the data the provider still holds and releases its resources. It returns when ctx is done
	// at the latest.
	Shutdown(ctx context.Context) error
}

// HTTPMetricsOptions configure HTTP server metrics.
type HTTPMetricsOptions struct {
	// Service is the service name recorded with each request.
	Service string
	// Route is the route recorded with each request; empty means the request's URL path.
	Route string
	// RouteFromRequest, when set, overrides Route per request.
	RouteFromRequest func(*http.Request) string
}

// SpanKind is the role of a span in a trace.
type SpanKind uint8

const (
	SpanKindInternal SpanKind = iota
	SpanKindServer
	SpanKindClient
)

// SpanOption configures a span when it starts.
type SpanOption struct {
	kind  SpanKind
	attrs []Attribute
}

// WithSpanKind sets the kind of the span; the default is SpanKindInternal.
func WithSpanKind(kind SpanKind) SpanOption {
	return SpanOption{kind: kind}
}

// WithAttributes sets attributes on the span when it starts.
func WithAttributes(attrs ...Attribute) SpanOption {
	return SpanOption{attrs: attrs}
}

// SpanConfig is the configuration of a span, as the options of Tracer.Start set it.
type SpanConfig struct {
	Kind       SpanKind
	Attributes []Attribute
}

// NewSpanConfig applies options in order.
func NewSpanConfig(opts ...SpanOption) SpanConfig {
	var c SpanConfig
	for _, o := range opts {
		if o.kind != SpanKindInternal {
			c.Kind = o.kind
		}
		c.Attributes = append(c.Attributes, o.attrs...)
	}
	return c
}

// Tracer starts spans.
type Tracer interface {
	// Start starts a span named name, as a child of the span in ctx, and returns a context holding it.
	Start(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span)
}

// Span is one operation of a trace. It is ended exactly once.
type Span interface {
	SetAttributes(attrs ...Attribute)
	// RecordError records err as an event of the span; it does not change the span's status.
	RecordError(err error)
	// SetError marks the span as failed with a description.
	SetError(description string)
	End()
}

// Meter creates instruments. Creating an instrument twice with the same name returns an equivalent instrument.
type Meter interface {
	Int64Counter(name string, opts ...InstrumentOption) Int64Counter
	Int64UpDownCounter(name string, opts ...InstrumentOption) Int64UpDownCounter
	Float64Histogram(name string, opts ...InstrumentOption) Float64Histogram
}

// GaugeMeter is implemented by a Meter that also creates gauges. A telemetry provider without gauges leaves them
// out, and what is recorded in gauges is not recorded.
type GaugeMeter interface {
	Int64Gauge(name string, opts ...InstrumentOption) Int64Gauge
}

// Int64Gauge records the latest value of a measurement.
type Int64Gauge interface {
	Record(ctx context.Context, value int64, attrs ...Attribute)
}

// Int64Counter records non-negative increments.
type Int64Counter interface {
	Add(ctx context.Context, incr int64, attrs ...Attribute)
}

// Int64UpDownCounter records increments and decrements.
type Int64UpDownCounter interface {
	Add(ctx context.Context, incr int64, attrs ...Attribute)
}

// Float64Histogram records a distribution of values.
type Float64Histogram interface {
	Record(ctx context.Context, value float64, attrs ...Attribute)
}

// InstrumentOption configures an instrument when it is created.
type InstrumentOption struct {
	description string
	unit        string
	buckets     []float64
}

// WithDescription sets the description of an instrument.
func WithDescription(description string) InstrumentOption {
	return InstrumentOption{description: description}
}

// WithUnit sets the unit of an instrument, such as "s" or "By".
func WithUnit(unit string) InstrumentOption {
	return InstrumentOption{unit: unit}
}

// WithBuckets sets the explicit bucket boundaries of a histogram.
func WithBuckets(boundaries ...float64) InstrumentOption {
	return InstrumentOption{buckets: boundaries}
}

// InstrumentConfig is the configuration of an instrument, as its options set it.
type InstrumentConfig struct {
	Description string
	Unit        string
	// Buckets are the explicit bucket boundaries of a histogram; nil means the provider's default.
	Buckets []float64
}

// NewInstrumentConfig applies options in order; a later option overrides an earlier one.
func NewInstrumentConfig(opts ...InstrumentOption) InstrumentConfig {
	var c InstrumentConfig
	for _, o := range opts {
		if o.description != "" {
			c.Description = o.description
		}
		if o.unit != "" {
			c.Unit = o.unit
		}
		if o.buckets != nil {
			c.Buckets = o.buckets
		}
	}
	return c
}

// Factory creates a telemetry provider.
type Factory interface {
	Type() string
	// Create builds the provider from settings. An invalid configuration is an error that names the faulty value.
	Create(ctx context.Context, settings Settings) (Provider, error)
}

// Settings are what a factory gets to build a provider.
type Settings struct {
	Logger *logger.Logger
	// DistributionName and DistributionVersion identify the distribution that records, such as "flagd" and its
	// version.
	DistributionName    string
	DistributionVersion string
	Config              map[string]any
}

// ValidateDeclaration checks what a factory declares: a non-empty type name.
func ValidateDeclaration(f Factory) error {
	if f == nil {
		return errors.New("the factory is nil")
	}
	if strings.TrimSpace(f.Type()) == "" {
		return errors.New("the type name is empty")
	}
	return nil
}
