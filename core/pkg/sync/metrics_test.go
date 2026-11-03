package sync

import (
	"context"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/stretchr/testify/require"
)

const (
	testURI      = "grpc://features-api.example:8015"
	testSelector = "all-flags"
)

// measurement is one call on an instrument of recordingMeter.
type measurement struct {
	name  string
	value int64
	attrs map[string]string
}

// recordingMeter keeps what is recorded on its instruments.
type recordingMeter struct {
	measurements []measurement
	instruments  map[string]instrumentSpec
}

type instrumentSpec struct {
	kind        string
	description string
	unit        string
}

func newRecordingMeter() *recordingMeter {
	return &recordingMeter{instruments: map[string]instrumentSpec{}}
}

func (m *recordingMeter) record(name string, value int64, attrs []telemetry.Attribute) {
	got := map[string]string{}
	for _, a := range attrs {
		got[a.Key] = a.Value.AsString()
	}
	m.measurements = append(m.measurements, measurement{name: name, value: value, attrs: got})
}

func (m *recordingMeter) named(name string) []measurement {
	var out []measurement
	for _, x := range m.measurements {
		if x.name == name {
			out = append(out, x)
		}
	}
	return out
}

func (m *recordingMeter) declare(kind, name string, opts []telemetry.InstrumentOption) {
	c := telemetry.NewInstrumentConfig(opts...)
	m.instruments[name] = instrumentSpec{kind: kind, description: c.Description, unit: c.Unit}
}

type recordingInstrument struct {
	m    *recordingMeter
	name string
}

func (i recordingInstrument) Add(_ context.Context, v int64, attrs ...telemetry.Attribute) {
	i.m.record(i.name, v, attrs)
}

func (i recordingInstrument) Record(_ context.Context, v int64, attrs ...telemetry.Attribute) {
	i.m.record(i.name, v, attrs)
}

func (m *recordingMeter) Int64Counter(name string, opts ...telemetry.InstrumentOption) telemetry.Int64Counter {
	m.declare("counter", name, opts)
	return recordingInstrument{m, name}
}

func (m *recordingMeter) Int64UpDownCounter(name string, opts ...telemetry.InstrumentOption) telemetry.Int64UpDownCounter {
	m.declare("updown", name, opts)
	return recordingInstrument{m, name}
}

func (m *recordingMeter) Float64Histogram(string, ...telemetry.InstrumentOption) telemetry.Float64Histogram {
	return telemetry.Noop().Meter("").Float64Histogram("")
}

// withGauges is the meter of a telemetry provider that has gauges.
type withGauges struct{ *recordingMeter }

func (m withGauges) Int64Gauge(name string, opts ...telemetry.InstrumentOption) telemetry.Int64Gauge {
	m.declare("gauge", name, opts)
	return recordingInstrument{m.recordingMeter, name}
}

func newRecorder(gauges bool) (MetricsRecorder, *recordingMeter) {
	m := newRecordingMeter()
	if gauges {
		return NewMetricsRecorder(withGauges{m}), m
	}
	return NewMetricsRecorder(m), m
}

func TestRecordFlagConfigReceivedCountsAndDatesThePayload(t *testing.T) {
	r, m := newRecorder(true)
	ctx := context.Background()

	before := time.Now().Unix()
	for range 3 {
		r.RecordFlagConfigReceived(ctx, SourceGRPC, testURI, testSelector)
	}
	after := time.Now().Unix()

	counts := m.named(metricFlagConfigReceived)
	require.Len(t, counts, 3)
	require.Empty(t, m.instruments[metricFlagConfigReceived].unit, "the counter has no unit")

	stamps := m.named(metricFlagConfigLastReceivedTimestamp)
	require.Len(t, stamps, 3)
	require.Equal(t, "gauge", m.instruments[metricFlagConfigLastReceivedTimestamp].kind)
	require.Equal(t, "s", m.instruments[metricFlagConfigLastReceivedTimestamp].unit)
	for _, s := range stamps {
		require.GreaterOrEqual(t, s.value, before, "the timestamp must not predate the first call")
		require.LessOrEqual(t, s.value, after, "the timestamp must not postdate the last call")
	}
}

func TestRecordFlagConfigReceivedWithoutGaugesCountsOnly(t *testing.T) {
	r, m := newRecorder(false)

	r.RecordFlagConfigReceived(context.Background(), SourceFile, "file:./flags.json", "")

	require.Len(t, m.named(metricFlagConfigReceived), 1)
	require.Empty(t, m.named(metricFlagConfigLastReceivedTimestamp))
}

func TestRecordFlagConfigReceivedEmitsDualAttributeSet(t *testing.T) {
	r, m := newRecorder(true)
	r.RecordFlagConfigReceived(context.Background(), SourceGRPC, testURI, testSelector)

	require.Equal(t, map[string]string{
		AttrLegacyProvider: SourceGRPC, AttrLegacyURI: testURI, AttrLegacySelector: testSelector,
		AttrType: SourceGRPC, AttrURI: testURI, AttrSelector: testSelector,
	}, m.named(metricFlagConfigReceived)[0].attrs)
}

func TestRecordFlagConfigReceivedOmitsSelectorAttributesWhenEmpty(t *testing.T) {
	r, m := newRecorder(true)
	r.RecordFlagConfigReceived(context.Background(), SourceHTTP, testURI, "")

	require.Equal(t, map[string]string{
		AttrLegacyProvider: SourceHTTP, AttrLegacyURI: testURI, AttrType: SourceHTTP, AttrURI: testURI,
	}, m.named(metricFlagConfigReceived)[0].attrs)
}

func TestRecordFlagConfigReceivedRedactsSecretsInURI(t *testing.T) {
	r, m := newRecorder(true)

	r.RecordFlagConfigReceived(context.Background(), SourceHTTP,
		"https://user:hunter2@api.example.com/flags?token=SECRET_TOKEN", "")

	attrs := m.named(metricFlagConfigReceived)[0].attrs
	require.Equal(t, "https://api.example.com/flags", attrs[AttrURI])
	require.Equal(t, "https://api.example.com/flags", attrs[AttrLegacyURI])
	for key, value := range attrs {
		require.NotContains(t, value, "hunter2", "userinfo password leaked to attribute %q", key)
		require.NotContains(t, value, "SECRET_TOKEN", "query token leaked to attribute %q", key)
	}
}

func TestRecordFlagConfigReceivedThroughNoopMeterIsSafe(t *testing.T) {
	NewMetricsRecorder(telemetry.Noop().Meter(MetricsScope)).
		RecordFlagConfigReceived(context.Background(), SourceGRPC, testURI, testSelector)
	RecordFlagConfigReceived(context.Background(), nil, SourceGRPC, testURI, testSelector)
}

func TestSanitizeURI(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"clean grpc", "grpc://sync.internal:9090", "grpc://sync.internal:9090"},
		{"clean http", "https://api.example.com/flags", "https://api.example.com/flags"},
		{"clean file path", "file:./demo.flagd.json", "file:./demo.flagd.json"},

		{"strips query token", "https://api.example.com/flags?token=abc123", "https://api.example.com/flags"},
		{
			"strips signed s3 url",
			"https://bucket.s3.amazonaws.com/flags.json?X-Amz-Signature=deadbeef&X-Amz-Credential=AKIAEXAMPLE",
			"https://bucket.s3.amazonaws.com/flags.json",
		},
		{"strips userinfo", "https://user:pass@host/flags", "https://host/flags"},
		{"strips userinfo (username only)", "https://user@host/flags", "https://host/flags"},
		{"strips fragment", "grpc://sync.internal:9090#region-eu", "grpc://sync.internal:9090"},
		{
			"strips everything combined",
			"https://user:pass@api.example.com/flags?token=abc#top",
			"https://api.example.com/flags",
		},

		// Fail-closed: url.Parse rejects these, so we return "" rather than a
		// partially-sanitized string that could still preserve userinfo.
		{"malformed escape with userinfo fails closed", "https://user:hunter2@api.example.com/flags%ZZ", ""},
		{"control character fails closed", "https://user:hunter2@api.example.com/flags\x7f", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeURI(tc.in)
			require.Equal(t, tc.want, got)
			// no known secret marker from a malformed input survives into the output
			require.NotContains(t, got, "hunter2", "userinfo password leaked from %q", tc.in)
		})
	}
}
