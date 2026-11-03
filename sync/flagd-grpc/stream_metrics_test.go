package flagdgrpc

import (
	"context"
	"testing"

	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/stretchr/testify/require"
)

const (
	testURI      = "grpc://features-api.example:8015"
	testSelector = "all-flags"
)

// call is one measurement on an instrument of recordingMeter.
type call struct {
	instrument string
	value      int64
	attrs      map[string]string
}

// recordingMeter keeps what is recorded on its instruments.
type recordingMeter struct {
	calls []call
	specs map[string]telemetry.InstrumentConfig
}

func (m *recordingMeter) record(name string, value int64, attrs []telemetry.Attribute) {
	got := map[string]string{}
	for _, a := range attrs {
		got[a.Key] = a.Value.AsString()
	}
	m.calls = append(m.calls, call{instrument: name, value: value, attrs: got})
}

func (m *recordingMeter) callsOf(name string) []call {
	var out []call
	for _, c := range m.calls {
		if c.instrument == name {
			out = append(out, c)
		}
	}
	return out
}

type instrument struct {
	m    *recordingMeter
	name string
}

func (i instrument) Add(_ context.Context, v int64, attrs ...telemetry.Attribute) {
	i.m.record(i.name, v, attrs)
}

func (m *recordingMeter) Int64Counter(name string, opts ...telemetry.InstrumentOption) telemetry.Int64Counter {
	m.specs[name] = telemetry.NewInstrumentConfig(opts...)
	return instrument{m, name}
}

func (m *recordingMeter) Int64UpDownCounter(name string, opts ...telemetry.InstrumentOption) telemetry.Int64UpDownCounter {
	m.specs[name] = telemetry.NewInstrumentConfig(opts...)
	return instrument{m, name}
}

func (m *recordingMeter) Float64Histogram(string, ...telemetry.InstrumentOption) telemetry.Float64Histogram {
	return telemetry.Noop().Meter("").Float64Histogram("")
}

func newStreamMetrics() (StreamMetricsRecorder, *recordingMeter) {
	m := &recordingMeter{specs: map[string]telemetry.InstrumentConfig{}}
	return NewStreamMetrics(m), m
}

func TestStreamOpenCloseSymmetry(t *testing.T) {
	r, m := newStreamMetrics()
	ctx := context.Background()

	// 3 opens - 2 closes = 1 stream currently open
	for range 3 {
		r.RecordStreamOpened(ctx, testURI, testSelector)
	}
	for range 2 {
		r.RecordStreamClosed(ctx, testURI, testSelector)
	}

	var open int64
	for _, c := range m.callsOf(metricClientStreamActive) {
		open += c.value
	}
	require.EqualValues(t, 1, open)
	require.Empty(t, m.specs[metricClientStreamActive].Unit)
}

func TestStreamReconnectsCount(t *testing.T) {
	r, m := newStreamMetrics()

	for range 4 {
		r.RecordReconnect(context.Background(), testURI, testSelector)
	}

	reconnects := m.callsOf(metricClientStreamReconnects)
	require.Len(t, reconnects, 4)
	for _, c := range reconnects {
		require.EqualValues(t, 1, c.value, "a reconnect counter only counts up")
	}
}

func TestStreamMetricsEmitDualAttributeSetOfTheGRPCSource(t *testing.T) {
	r, m := newStreamMetrics()
	r.RecordStreamOpened(context.Background(), testURI, testSelector)

	require.Equal(t, map[string]string{
		coresync.AttrLegacyProvider: coresync.SourceGRPC, coresync.AttrLegacyURI: testURI,
		coresync.AttrLegacySelector: testSelector,
		coresync.AttrType:           coresync.SourceGRPC, coresync.AttrURI: testURI,
		coresync.AttrSelector: testSelector,
	}, m.callsOf(metricClientStreamActive)[0].attrs)
}

func TestStreamMetricsOmitSelectorWhenEmpty(t *testing.T) {
	r, m := newStreamMetrics()
	r.RecordReconnect(context.Background(), testURI, "")

	attrs := m.callsOf(metricClientStreamReconnects)[0].attrs
	require.NotContains(t, attrs, coresync.AttrLegacySelector)
	require.NotContains(t, attrs, coresync.AttrSelector)
}

func TestStreamMetricsRedactSecretsInURI(t *testing.T) {
	r, m := newStreamMetrics()

	r.RecordStreamOpened(context.Background(), "grpc://user:hunter2@sync.internal:9090?token=SECRET_TOKEN", testSelector)

	attrs := m.callsOf(metricClientStreamActive)[0].attrs
	require.Equal(t, "grpc://sync.internal:9090", attrs[coresync.AttrURI])
	require.Equal(t, "grpc://sync.internal:9090", attrs[coresync.AttrLegacyURI])
	for key, value := range attrs {
		require.NotContains(t, value, "hunter2", "userinfo password leaked to %q", key)
		require.NotContains(t, value, "SECRET_TOKEN", "query token leaked to %q", key)
	}
}
