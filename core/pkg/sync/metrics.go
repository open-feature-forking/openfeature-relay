package sync

import "context"

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
// implementation, such as the OpenTelemetry one in the telemetry package; sync providers import no
// telemetry themselves.
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
