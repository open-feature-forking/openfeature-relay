package flagdgrpc

import "context"

// StreamMetricsRecorder records the lifecycle of the sync stream of a gRPC source. A distribution
// injects an implementation, such as the OpenTelemetry one in the telemetry package; this package
// imports no telemetry itself.
type StreamMetricsRecorder interface {
	// RecordStreamOpened notes that a SyncFlags stream opened.
	RecordStreamOpened(ctx context.Context, uri, selector string)
	// RecordStreamClosed notes that a SyncFlags stream ended.
	RecordStreamClosed(ctx context.Context, uri, selector string)
	// RecordReconnect notes a successful re-establishment of the stream after a failure.
	RecordReconnect(ctx context.Context, uri, selector string)
}

func (g *Sync) streamOpened(ctx context.Context) {
	if g.StreamMetricsRecorder != nil {
		g.StreamMetricsRecorder.RecordStreamOpened(ctx, g.URI, g.Selector)
	}
}

func (g *Sync) streamClosed(ctx context.Context) {
	if g.StreamMetricsRecorder != nil {
		g.StreamMetricsRecorder.RecordStreamClosed(ctx, g.URI, g.Selector)
	}
}

func (g *Sync) streamReconnected(ctx context.Context) {
	if g.StreamMetricsRecorder != nil {
		g.StreamMetricsRecorder.RecordReconnect(ctx, g.URI, g.Selector)
	}
}
