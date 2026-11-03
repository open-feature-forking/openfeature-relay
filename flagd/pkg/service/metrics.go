package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// MetricsScope is the instrumentation scope of the metrics the service endpoints record. The OpenTelemetry
// telemetry provider sets the buckets of the histograms of this scope.
const MetricsScope = "flagd"

const (
	httpRequestDurationMetric = "http.server.request.duration"
	httpResponseSizeMetric    = "http.server.response.body.size"
	httpActiveRequestsMetric  = "http.server.active_requests"

	providerName     = "flagd"
	impressionMetric = "feature_flag." + providerName + ".impression"
	reasonMetric     = "feature_flag." + providerName + ".result.reason"

	syncServerActiveStreamsMetric  = "feature_flag." + providerName + ".sync.server.active_streams"
	syncServerStreamDurationMetric = "feature_flag." + providerName + ".sync.server.stream.duration"

	flagSetIDMetadataKey = "flagSetId"
)

// Attribute keys of the evaluation metrics and spans.
const (
	FeatureFlagReasonKey = "feature_flag.reason"
	exceptionTypeKey     = "ExceptionTypeKeyName"
	featureFlagKey       = "feature_flag.key"
	featureFlagVariant   = "feature_flag.result.variant"
	featureFlagProvider  = "feature_flag.provider.name"
	featureFlagSetID     = "feature_flag.set.id"
)

// Buckets of the histograms: seconds for the request duration, bytes for the response size, and seconds for the
// duration of a sync stream.
var (
	requestDurationBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}
	responseSizeBuckets    = []float64{100, 1_000, 10_000, 100_000, 1_000_000, 10_000_000, 100_000_000, 1_000_000_000}
	streamDurationBuckets  = []float64{30, 60, 120, 300, 480, 600, 1200, 1800, 3600, 10800}
)

// Metrics records the metrics of the service endpoints: HTTP requests, evaluations and the sync streams of the
// flag sync endpoint. It creates its instruments on a telemetry.Meter, under flagd's metric names.
type Metrics struct {
	httpRequestDuration  telemetry.Float64Histogram
	httpResponseSize     telemetry.Float64Histogram
	httpRequestsInFlight telemetry.Int64UpDownCounter
	impressions          telemetry.Int64Counter
	reasons              telemetry.Int64Counter

	syncActiveStreams  telemetry.Int64UpDownCounter
	syncStreamDuration telemetry.Float64Histogram
}

// NewMetrics creates the instruments of the service endpoints on meter.
func NewMetrics(meter telemetry.Meter) *Metrics {
	return &Metrics{
		httpRequestDuration: meter.Float64Histogram(httpRequestDurationMetric,
			telemetry.WithDescription("Measures the duration of inbound HTTP requests."),
			telemetry.WithUnit("s"), telemetry.WithBuckets(requestDurationBuckets...)),
		httpResponseSize: meter.Float64Histogram(httpResponseSizeMetric,
			telemetry.WithDescription("Measures the size of HTTP request messages (compressed)."),
			telemetry.WithUnit("By"), telemetry.WithBuckets(responseSizeBuckets...)),
		httpRequestsInFlight: meter.Int64UpDownCounter(httpActiveRequestsMetric,
			telemetry.WithDescription("Measures the number of concurrent HTTP requests that are currently in-flight."),
			telemetry.WithUnit("{request}")),
		impressions: meter.Int64Counter(impressionMetric,
			telemetry.WithDescription("Measures the number of evaluations for a given flag."),
			telemetry.WithUnit("{impression}")),
		reasons: meter.Int64Counter(reasonMetric,
			telemetry.WithDescription("Measures the number of evaluations for a given reason."),
			telemetry.WithUnit("{reason}")),
		syncActiveStreams: meter.Int64UpDownCounter(syncServerActiveStreamsMetric,
			telemetry.WithDescription("Measures the number of currently active gRPC sync streaming connections."),
			telemetry.WithUnit("{stream}")),
		syncStreamDuration: meter.Float64Histogram(syncServerStreamDurationMetric,
			telemetry.WithDescription("Measures the duration of gRPC sync streaming connections."),
			telemetry.WithUnit("s"), telemetry.WithBuckets(streamDurationBuckets...)),
	}
}

// NoopMetrics returns Metrics that record nothing.
func NoopMetrics() *Metrics { return NewMetrics(telemetry.Noop().Meter(MetricsScope)) }

// HTTPAttributes returns the attributes of the HTTP server metrics, with a standardized method.
func HTTPAttributes(svcName, url, method, code, scheme string) []telemetry.Attribute {
	return []telemetry.Attribute{
		telemetry.String("service.name", svcName),
		telemetry.String("http.route", url),
		telemetry.String("http.request.method", standardizeHTTPMethod(method)),
		telemetry.String("http.response.status_code", code),
		telemetry.String("url.scheme", scheme),
	}
}

// standardizeHTTPMethod canonicalizes known HTTP methods and maps unknown ones to _OTHER.
func standardizeHTTPMethod(method string) string {
	// standard methods are at most 7 bytes; skip ToUpper's copy for anything longer
	if len(method) > len(http.MethodConnect) {
		return "_OTHER"
	}
	method = strings.ToUpper(method)
	switch method {
	case http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead,
		http.MethodOptions, http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodTrace:
		return method
	default:
		return "_OTHER"
	}
}

// HTTPRequestDuration records the duration of an HTTP request.
func (m *Metrics) HTTPRequestDuration(ctx context.Context, duration time.Duration, attrs []telemetry.Attribute) {
	m.httpRequestDuration.Record(ctx, duration.Seconds(), attrs...)
}

// HTTPResponseSize records the size of an HTTP response.
func (m *Metrics) HTTPResponseSize(ctx context.Context, sizeBytes int64, attrs []telemetry.Attribute) {
	m.httpResponseSize.Record(ctx, float64(sizeBytes), attrs...)
}

// InFlightRequestStart counts an HTTP request that started.
func (m *Metrics) InFlightRequestStart(ctx context.Context, attrs []telemetry.Attribute) {
	m.httpRequestsInFlight.Add(ctx, 1, attrs...)
}

// InFlightRequestEnd counts an HTTP request that ended.
func (m *Metrics) InFlightRequestEnd(ctx context.Context, attrs []telemetry.Attribute) {
	m.httpRequestsInFlight.Add(ctx, -1, attrs...)
}

// RecordEvaluation records an evaluation: an impression when it succeeded, and its reason.
func (m *Metrics) RecordEvaluation(
	ctx context.Context, err error, reason, variant, key string, metadata model.Metadata,
) {
	if err == nil {
		m.Impressions(ctx, reason, variant, key, metadata)
	}
	m.Reasons(ctx, key, reason, metadata, err)
}

// Impressions counts an evaluation of a flag.
func (m *Metrics) Impressions(ctx context.Context, reason, variant, key string, metadata model.Metadata) {
	attrs := append(SemConvFeatureFlagAttributes(key, variant), telemetry.String(FeatureFlagReasonKey, reason))
	attrs = appendFlagSetID(attrs, metadata)
	m.impressions.Add(ctx, 1, attrs...)
}

// Reasons counts an evaluation under its reason.
func (m *Metrics) Reasons(ctx context.Context, key, reason string, metadata model.Metadata, err error) {
	attrs := []telemetry.Attribute{
		telemetry.String(featureFlagProvider, providerName),
		telemetry.String(FeatureFlagReasonKey, reason),
	}
	if err == nil {
		// record flag key only if evaluation is successful
		attrs = append(attrs, telemetry.String(featureFlagKey, key))
	} else {
		attrs = append(attrs, telemetry.String(exceptionTypeKey, err.Error()))
	}
	if err == nil || err.Error() != model.FlagNotFoundErrorCode {
		attrs = appendFlagSetID(attrs, metadata)
	}
	m.reasons.Add(ctx, 1, attrs...)
}

// SyncServerStreamStart counts a sync stream that opened.
func (m *Metrics) SyncServerStreamStart(ctx context.Context, attrs []telemetry.Attribute) {
	m.syncActiveStreams.Add(ctx, 1, attrs...)
}

// SyncServerStreamEnd counts a sync stream that closed.
func (m *Metrics) SyncServerStreamEnd(ctx context.Context, attrs []telemetry.Attribute) {
	m.syncActiveStreams.Add(ctx, -1, attrs...)
}

// SyncServerStreamDuration records how long a sync stream was open.
func (m *Metrics) SyncServerStreamDuration(ctx context.Context, duration time.Duration, attrs []telemetry.Attribute) {
	m.syncStreamDuration.Record(ctx, duration.Seconds(), attrs...)
}

// SemConvFeatureFlagAttributes returns the feature flag attributes of the OpenTelemetry semantic conventions.
// See https://opentelemetry.io/docs/reference/specification/trace/semantic_conventions/feature-flags/
func SemConvFeatureFlagAttributes(key, variant string) []telemetry.Attribute {
	return []telemetry.Attribute{
		telemetry.String(featureFlagKey, key),
		telemetry.String(featureFlagVariant, variant),
		telemetry.String(featureFlagProvider, providerName),
	}
}

func appendFlagSetID(attrs []telemetry.Attribute, metadata model.Metadata) []telemetry.Attribute {
	if id, ok := metadata[flagSetIDMetadataKey].(string); ok && id != "" {
		return append(attrs, telemetry.String(featureFlagSetID, id))
	}
	return attrs
}
