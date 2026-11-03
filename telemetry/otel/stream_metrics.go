package telemetry

import (
	"context"

	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// StreamRecorder owns the gRPC-client-specific stream-lifecycle instruments. These
// complement the source-agnostic SyncRecorder — the universal
// `flag_config.received` counter fires from every sync provider, but "is my stream open
// right now?" and "how often did it drop and reconnect?" for stream-based providers

const streamMeterName = "github.com/open-feature/flagd/core/pkg/sync/grpc"

const (
	metricClientStreamActive     = "feature_flag.flagd.sync.client.stream.active"
	metricClientStreamReconnects = "feature_flag.flagd.sync.client.stream.reconnects"
)

type StreamRecorder struct {
	streamActive     metric.Int64UpDownCounter
	streamReconnects metric.Int64Counter
}

// NewStreamRecorder builds the gRPC stream-lifecycle recorder against the given
// MeterProvider. A nil MeterProvider falls back to the global one (which telemetry
// registers via otel.SetMeterProvider), and if that too is a noop the returned recorder
// degrades to no-ops — so callers never have to check.
func NewStreamRecorder(mp metric.MeterProvider) *StreamRecorder {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	if mp == nil {
		mp = noop.NewMeterProvider()
	}
	m := mp.Meter(streamMeterName)

	// No WithUnit on either instrument: the OTel Prometheus exporter mangles UCUM
	// annotation units (e.g. "{stream}", "{reconnect}") into "__stream__" / "__reconnect__"
	// suffixes in the Prometheus name. Description already spells out the units.
	streamActive, _ := m.Int64UpDownCounter(
		metricClientStreamActive,
		metric.WithDescription(
			"Number of currently open flagd -> flag-server gRPC sync streams "+
				"(1 while a SyncFlags stream is open, 0 when disconnected)."),
	)
	streamReconnects, _ := m.Int64Counter(
		metricClientStreamReconnects,
		metric.WithDescription(
			"Total number of successful sync stream reconnections after an initial failure. "+
				"Steady growth indicates upstream flapping."),
	)
	return &StreamRecorder{
		streamActive:     streamActive,
		streamReconnects: streamReconnects,
	}
}

// RecordStreamOpened bumps the active-streams gauge by 1 when a SyncFlags stream opens.
func (m *StreamRecorder) RecordStreamOpened(ctx context.Context, uri, selector string) {
	if m == nil || m.streamActive == nil {
		return
	}
	m.streamActive.Add(ctx, 1, streamAttrs(uri, selector))
}

// RecordStreamClosed decrements the active-streams gauge when the SyncFlags stream ends.
func (m *StreamRecorder) RecordStreamClosed(ctx context.Context, uri, selector string) {
	if m == nil || m.streamActive == nil {
		return
	}
	m.streamActive.Add(ctx, -1, streamAttrs(uri, selector))
}

// RecordReconnect increments the reconnect counter on each successful re-establishment.
func (m *StreamRecorder) RecordReconnect(ctx context.Context, uri, selector string) {
	if m == nil || m.streamReconnects == nil {
		return
	}
	m.streamReconnects.Add(ctx, 1, streamAttrs(uri, selector))
}

// streamAttrs assembles the same dual attribute set that syncmetrics uses for
// source-agnostic recording, with the sync source hard-wired to "grpc" since these
// instruments only ever live in the gRPC provider.
func streamAttrs(uri, selector string) metric.MeasurementOption {
	safeURI := SanitizeURI(uri)
	kvs := make([]attribute.KeyValue, 0, 6)
	kvs = append(kvs,
		attribute.String(AttrLegacyProvider, coresync.SourceGRPC),
		attribute.String(AttrLegacyURI, safeURI),
		attribute.String(AttrType, coresync.SourceGRPC),
		attribute.String(AttrURI, safeURI),
	)
	if selector != "" {
		kvs = append(kvs,
			attribute.String(AttrLegacySelector, selector),
			attribute.String(AttrSelector, selector),
		)
	}
	return metric.WithAttributes(kvs...)
}
