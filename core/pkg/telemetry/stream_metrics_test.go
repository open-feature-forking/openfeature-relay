package telemetry

import (
	"context"
	"testing"

	coresync "github.com/open-feature/flagd/core/pkg/sync"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	msdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func newTestClientStreamMetrics(t *testing.T) (*StreamRecorder, *msdk.ManualReader) {
	t.Helper()
	reader := msdk.NewManualReader()
	mp := msdk.NewMeterProvider(msdk.WithReader(reader))
	return NewStreamRecorder(mp), reader
}

func collectClientStream(t *testing.T, reader *msdk.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	return rm
}

func findClientStreamMetric(rm metricdata.ResourceMetrics, name string) *metricdata.Metrics {
	for i := range rm.ScopeMetrics {
		for j := range rm.ScopeMetrics[i].Metrics {
			if rm.ScopeMetrics[i].Metrics[j].Name == name {
				return &rm.ScopeMetrics[i].Metrics[j]
			}
		}
	}
	return nil
}

func TestNewClientStreamMetrics_NilProviderIsSafe(t *testing.T) {
	m := NewStreamRecorder(nil)
	require.NotNil(t, m)
	// Method calls must be safe even when the recorder was built without a MeterProvider.
	m.RecordStreamOpened(context.Background(), testURI, testSelector)
	m.RecordStreamClosed(context.Background(), testURI, testSelector)
	m.RecordReconnect(context.Background(), testURI, testSelector)
}

func TestClientStreamMetrics_NilReceiverIsSafe(t *testing.T) {
	var m *StreamRecorder
	m.RecordStreamOpened(context.Background(), testURI, testSelector)
	m.RecordStreamClosed(context.Background(), testURI, testSelector)
	m.RecordReconnect(context.Background(), testURI, testSelector)
}

func TestClientStreamMetrics_StreamOpenCloseSymmetry(t *testing.T) {
	m, reader := newTestClientStreamMetrics(t)
	ctx := context.Background()

	// 3 opens - 2 closes = 1 stream currently open.
	m.RecordStreamOpened(ctx, testURI, testSelector)
	m.RecordStreamOpened(ctx, testURI, testSelector)
	m.RecordStreamOpened(ctx, testURI, testSelector)
	m.RecordStreamClosed(ctx, testURI, testSelector)
	m.RecordStreamClosed(ctx, testURI, testSelector)

	rm := collectClientStream(t, reader)
	metric := findClientStreamMetric(rm, metricClientStreamActive)
	require.NotNil(t, metric, "expected metric %s", metricClientStreamActive)

	sum, ok := metric.Data.(metricdata.Sum[int64])
	require.True(t, ok, "expected UpDownCounter -> Sum[int64]")
	require.False(t, sum.IsMonotonic, "stream.active is an UpDownCounter, not monotonic")
	require.Len(t, sum.DataPoints, 1)
	require.EqualValues(t, 1, sum.DataPoints[0].Value)
}

func TestClientStreamMetrics_ReconnectsAreMonotonic(t *testing.T) {
	m, reader := newTestClientStreamMetrics(t)
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		m.RecordReconnect(ctx, testURI, testSelector)
	}

	rm := collectClientStream(t, reader)
	metric := findClientStreamMetric(rm, metricClientStreamReconnects)
	require.NotNil(t, metric, "expected metric %s", metricClientStreamReconnects)

	sum, ok := metric.Data.(metricdata.Sum[int64])
	require.True(t, ok, "expected Counter -> Sum[int64]")
	require.True(t, sum.IsMonotonic, "reconnects counter must be monotonic")
	require.Len(t, sum.DataPoints, 1)
	require.EqualValues(t, 4, sum.DataPoints[0].Value)
}

func TestClientStreamMetrics_EmitsDualAttributeSet(t *testing.T) {
	m, reader := newTestClientStreamMetrics(t)
	m.RecordStreamOpened(context.Background(), testURI, testSelector)

	rm := collectClientStream(t, reader)
	metric := findClientStreamMetric(rm, metricClientStreamActive)
	require.NotNil(t, metric)
	set := metric.Data.(metricdata.Sum[int64]).DataPoints[0].Attributes

	// Legacy (kept for back-compat) — source hard-wired to "grpc" in this package.
	assertClientStreamAttr(t, set, AttrLegacyProvider, coresync.SourceGRPC)
	assertClientStreamAttr(t, set, AttrLegacyURI, testURI)
	assertClientStreamAttr(t, set, AttrLegacySelector, testSelector)

	// New OTel-aligned.
	assertClientStreamAttr(t, set, AttrType, coresync.SourceGRPC)
	assertClientStreamAttr(t, set, AttrURI, testURI)
	assertClientStreamAttr(t, set, AttrSelector, testSelector)
}

func TestClientStreamMetrics_SelectorOmittedWhenEmpty(t *testing.T) {
	m, reader := newTestClientStreamMetrics(t)
	m.RecordReconnect(context.Background(), testURI, "")

	rm := collectClientStream(t, reader)
	metric := findClientStreamMetric(rm, metricClientStreamReconnects)
	require.NotNil(t, metric)
	set := metric.Data.(metricdata.Sum[int64]).DataPoints[0].Attributes

	_, hasLegacy := set.Value(attribute.Key(AttrLegacySelector))
	require.False(t, hasLegacy, "expected no %s when selector is empty", AttrLegacySelector)
	_, hasNew := set.Value(attribute.Key(AttrSelector))
	require.False(t, hasNew, "expected no %s when selector is empty", AttrSelector)
}

func assertClientStreamAttr(t *testing.T, set attribute.Set, key, expected string) {
	t.Helper()
	v, ok := set.Value(attribute.Key(key))
	require.True(t, ok, "expected attribute %q", key)
	require.Equal(t, expected, v.AsString(), "attribute %q value", key)
}

// Mirrors the syncmetrics-side redaction test for the stream-lifecycle attribute set.
func TestClientStreamMetrics_RedactsSecretsInURI(t *testing.T) {
	m, reader := newTestClientStreamMetrics(t)

	const secretURI = "grpc://user:hunter2@sync.internal:9090?token=SECRET_TOKEN"
	const cleanURI = "grpc://sync.internal:9090"

	m.RecordStreamOpened(context.Background(), secretURI, testSelector)

	rm := collectClientStream(t, reader)
	metric := findClientStreamMetric(rm, metricClientStreamActive)
	require.NotNil(t, metric)
	set := metric.Data.(metricdata.Sum[int64]).DataPoints[0].Attributes

	assertClientStreamAttr(t, set, AttrURI, cleanURI)
	assertClientStreamAttr(t, set, AttrLegacyURI, cleanURI)

	for _, kv := range set.ToSlice() {
		require.NotContains(t, kv.Value.AsString(), "hunter2", "userinfo password leaked to %q", kv.Key)
		require.NotContains(t, kv.Value.AsString(), "SECRET_TOKEN", "query token leaked to %q", kv.Key)
	}
}
