package sync

import (
	"context"
	"net/url"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// Source identifies which sync provider produced an observation. Values are lowercase and match
// the config-time provider names used by the sync builder, so operators see the same term on their
// dashboards as in their flagd config.
const (
	SourceGRPC       = "grpc"
	SourceHTTP       = "http"
	SourceFile       = "file"
	SourceKubernetes = "kubernetes"
	SourceBlob       = "blob"
)

// MetricsRecorder records client-side observations of a sync source. A distribution injects an
// implementation, such as the one NewMetricsRecorder makes; sync providers import no telemetry themselves.
type MetricsRecorder interface {
	// RecordFlagConfigReceived notes that a flag configuration payload was received from the source and
	// handed off downstream. It does not imply the payload was applied.
	RecordFlagConfigReceived(ctx context.Context, sourceType, uri, selector string)
}

// RecordFlagConfigReceived records through r; a nil recorder records nothing.
func RecordFlagConfigReceived(ctx context.Context, r MetricsRecorder, sourceType, uri, selector string) {
	if r != nil {
		r.RecordFlagConfigReceived(ctx, sourceType, uri, selector)
	}
}

// MetricsScope is the instrumentation scope of the metrics NewMetricsRecorder records. The scope name is observable
// output, so it stays the literal it has always been wherever this code lives.
const MetricsScope = "github.com/open-feature/flagd/core/pkg/sync/syncmetrics"

// Metric names, the canonical (dotted) names.
const (
	metricFlagConfigReceived              = "feature_flag.flagd.sync.client.flag_config.received"
	metricFlagConfigLastReceivedTimestamp = "feature_flag.flagd.sync.client.flag_config.last_received_timestamp"
)

// Attribute keys. Two sets are recorded on every data point so existing dashboards keep working during the
// migration:
//
//   - flagd.sync.*                    (pre-existing convention, kept for back-compat)
//   - feature_flag.flagd.sync.*       (new OTel-aligned taxonomy)
//
// Dropping the old set is a coordinated breaking release later.
const (
	AttrLegacyProvider = "flagd.sync.provider"
	AttrLegacyURI      = "flagd.sync.uri"
	AttrLegacySelector = "flagd.sync.selector"

	AttrType     = "feature_flag.flagd.sync.type"
	AttrURI      = "feature_flag.flagd.sync.uri"
	AttrSelector = "feature_flag.flagd.sync.selector"
)

// NewMetricsRecorder returns the MetricsRecorder that records the source-agnostic client-side metrics of flagd's
// sync channel on meter: the number of flag configurations received, and when the last one was. One recorder is
// shared by all sync providers of a distribution; each passes its own source type, URI and selector on every call.
//
// The timestamp is a gauge. A meter without gauges (telemetry.GaugeMeter) records the count only.
func NewMetricsRecorder(meter telemetry.Meter) MetricsRecorder {
	// the counter deliberately omits WithUnit so the OTel Prometheus exporter doesn't mangle an annotation unit into
	// the name
	r := &meterRecorder{received: meter.Int64Counter(metricFlagConfigReceived,
		telemetry.WithDescription("Total number of flag-configuration payloads received from a sync source and "+
			"handed off downstream. Does not imply the payload was successfully applied "+
			"to the evaluator's store — see the runtime-level apply metric for that."))}
	if g, ok := meter.(telemetry.GaugeMeter); ok {
		r.lastReceived = g.Int64Gauge(metricFlagConfigLastReceivedTimestamp,
			telemetry.WithDescription("Unix timestamp (seconds) of the most recent flag-configuration payload received "+
				"from a sync source. Query staleness as time() - <value>."),
			telemetry.WithUnit("s"))
	}
	return r
}

type meterRecorder struct {
	received     telemetry.Int64Counter
	lastReceived telemetry.Int64Gauge
}

// RecordFlagConfigReceived bumps the received counter and updates the last-received timestamp for the sync source.
// The selector attributes are left out when the selector is empty.
func (r *meterRecorder) RecordFlagConfigReceived(ctx context.Context, sourceType, uri, selector string) {
	attrs := SourceAttributes(sourceType, uri, selector)
	r.received.Add(ctx, 1, attrs...)
	if r.lastReceived != nil {
		r.lastReceived.Record(ctx, time.Now().Unix(), attrs...)
	}
}

// SourceAttributes returns the dual attribute set of an observation of a sync source. The URI is sanitized, and the
// selector attributes are left out when the selector is empty.
func SourceAttributes(sourceType, uri, selector string) []telemetry.Attribute {
	safeURI := SanitizeURI(uri)
	attrs := make([]telemetry.Attribute, 0, 6)
	attrs = append(attrs,
		telemetry.String(AttrLegacyProvider, sourceType),
		telemetry.String(AttrLegacyURI, safeURI),
		telemetry.String(AttrType, sourceType),
		telemetry.String(AttrURI, safeURI),
	)
	if selector != "" {
		attrs = append(attrs,
			telemetry.String(AttrLegacySelector, selector),
			telemetry.String(AttrSelector, selector),
		)
	}
	return attrs
}

// SanitizeURI strips userinfo, query, and fragment from a sync-source URI so token- or credential-bearing URIs
// don't land as attribute values. Fails closed: any input url.Parse rejects returns "" rather than a partially
// sanitized string that could still preserve userinfo.
func SanitizeURI(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
