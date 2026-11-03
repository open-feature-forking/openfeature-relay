package flagdgrpc

import (
	"context"

	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// StreamMetricsRecorder records the lifecycle of the sync stream of a gRPC source. A distribution injects an
// implementation, such as the one NewStreamMetrics makes.
type StreamMetricsRecorder interface {
	// RecordStreamOpened notes that a SyncFlags stream opened.
	RecordStreamOpened(ctx context.Context, uri, selector string)
	// RecordStreamClosed notes that a SyncFlags stream ended.
	RecordStreamClosed(ctx context.Context, uri, selector string)
	// RecordReconnect notes a successful re-establishment of the stream after a failure.
	RecordReconnect(ctx context.Context, uri, selector string)
}

// StreamMetricsScope is the instrumentation scope of the metrics NewStreamMetrics records. The scope name is
// observable output, so it stays the literal it has always been wherever this code lives.
const StreamMetricsScope = "github.com/open-feature/flagd/core/pkg/sync/grpc"

const (
	metricClientStreamActive     = "feature_flag.flagd.sync.client.stream.active"
	metricClientStreamReconnects = "feature_flag.flagd.sync.client.stream.reconnects"
)

// NewStreamMetrics returns the StreamMetricsRecorder that records the stream-lifecycle metrics of the gRPC sync
// sources on meter. They complement the source-agnostic metrics of core/pkg/sync: "is my stream open right now?"
// and "how often did it drop and reconnect?". The attributes are those of the source-agnostic metrics, with the
// source hard-wired to "grpc".
func NewStreamMetrics(meter telemetry.Meter) StreamMetricsRecorder {
	// No unit on either instrument: the OTel Prometheus exporter mangles UCUM annotation units (such as "{stream}")
	// into "__stream__" suffixes in the Prometheus name. The description spells out the units.
	return streamMetrics{
		active: meter.Int64UpDownCounter(metricClientStreamActive,
			telemetry.WithDescription("Number of currently open flagd -> flag-server gRPC sync streams "+
				"(1 while a SyncFlags stream is open, 0 when disconnected).")),
		reconnects: meter.Int64Counter(metricClientStreamReconnects,
			telemetry.WithDescription("Total number of successful sync stream reconnections after an initial failure. "+
				"Steady growth indicates upstream flapping.")),
	}
}

type streamMetrics struct {
	active     telemetry.Int64UpDownCounter
	reconnects telemetry.Int64Counter
}

func (m streamMetrics) RecordStreamOpened(ctx context.Context, uri, selector string) {
	m.active.Add(ctx, 1, coresync.SourceAttributes(coresync.SourceGRPC, uri, selector)...)
}

func (m streamMetrics) RecordStreamClosed(ctx context.Context, uri, selector string) {
	m.active.Add(ctx, -1, coresync.SourceAttributes(coresync.SourceGRPC, uri, selector)...)
}

func (m streamMetrics) RecordReconnect(ctx context.Context, uri, selector string) {
	m.reconnects.Add(ctx, 1, coresync.SourceAttributes(coresync.SourceGRPC, uri, selector)...)
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
