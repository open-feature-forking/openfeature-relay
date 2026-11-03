package service

import (
	"context"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// MetricsScope is the instrumentation scope of the metrics the service endpoints record. The OpenTelemetry
// telemetry provider sets the buckets of the histograms of this scope.
const MetricsScope = "flagd"

const (
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

// streamDurationBuckets are the buckets, in seconds, of the duration of a sync stream.
var streamDurationBuckets = []float64{30, 60, 120, 300, 480, 600, 1200, 1800, 3600, 10800}

// Metrics records the evaluation metrics of the service endpoints and the sync stream metrics of the flag sync
// endpoint. It creates its instruments on a telemetry.Meter, under flagd's metric names. The HTTP server metrics
// are the telemetry provider's.
type Metrics struct {
	impressions telemetry.Int64Counter
	reasons     telemetry.Int64Counter

	syncActiveStreams  telemetry.Int64UpDownCounter
	syncStreamDuration telemetry.Float64Histogram
}

// NewMetrics creates the instruments of the service endpoints on meter.
func NewMetrics(meter telemetry.Meter) *Metrics {
	return &Metrics{
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
