package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	coretel "github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/metric/metricdata/metricdatatest"
)

// endpointInstrument is an instrument the service endpoints record through MetricsRecorder today, as the core
// Meter creates it: today's name, description, unit and buckets.
type endpointInstrument struct {
	name        string
	description string
	unit        string
	buckets     []float64
	kind        string
}

var endpointInstruments = []endpointInstrument{
	{httpRequestDurationMetric, "Measures the duration of inbound HTTP requests.", "s", prometheus.DefBuckets, "histogram"},
	{httpResponseSizeMetric, "Measures the size of HTTP request messages (compressed).", "By", prometheus.ExponentialBuckets(100, 10, 8), "histogram"},
	{httpActiveRequestsMetric, "Measures the number of concurrent HTTP requests that are currently in-flight.", "{request}", nil, "updown"},
	{impressionMetric, "Measures the number of evaluations for a given flag.", "{impression}", nil, "counter"},
	{reasonMetric, "Measures the number of evaluations for a given reason.", "{reason}", nil, "counter"},
	{syncServerActiveStreamsMetric, "Measures the number of currently active gRPC sync streaming connections.", "{stream}", nil, "updown"},
	{syncServerStreamDurationMetric, "Measures the duration of gRPC sync streaming connections.", "s", []float64{30, 60, 120, 300, 480, 600, 1200, 1800, 3600, 10800}, "histogram"},
}

// TestMeterMatchesRecorder records the same measurements through today's MetricsRecorder and through the core Meter
// of the provider, under scopes with and without flagd's views, and requires identical metric data: names,
// descriptions, units, bucket boundaries, attributes and values.
func TestMeterMatchesRecorder(t *testing.T) {
	ctx := context.Background()
	httpAttrs := (MetricsRecorder{}).HTTPAttributes("flagd", "/ofrep/v1/evaluate/flags", "POST", "200", "http")
	metadata := model.Metadata{"flagSetId": "set"}

	// today: the recorder on a meter provider with flagd's views
	todayReader := metric.NewManualReader()
	today := newProvider(t, nil, todayReader)
	r := today.recorder
	r.HTTPRequestDuration(ctx, 250*time.Millisecond, httpAttrs)
	r.HTTPResponseSize(ctx, 1234, httpAttrs)
	r.InFlightRequestStart(ctx, httpAttrs)
	r.RecordEvaluation(ctx, nil, "STATIC", "on", "flag", metadata)
	r.RecordEvaluation(ctx, errors.New(model.FlagNotFoundErrorCode), "ERROR", "", "missing", nil)
	r.SyncServerStreamStart(ctx, []attribute.KeyValue{attribute.String("selector", "s")})
	r.SyncServerStreamDuration(ctx, 90*time.Second, []attribute.KeyValue{attribute.String("selector", "s")})

	// through the core API, under a scope flagd's views do not match, so only WithBuckets sets the boundaries
	coreReader := metric.NewManualReader()
	p := newProvider(t, nil, coreReader)
	m := p.Meter("flagd.endpoints")
	core := func(attrs []attribute.KeyValue) []coretel.Attribute {
		out := make([]coretel.Attribute, len(attrs))
		for i, kv := range attrs {
			out[i] = coretel.String(string(kv.Key), kv.Value.AsString())
		}
		return out
	}
	instrument := map[string]endpointInstrument{}
	for _, in := range endpointInstruments {
		instrument[in.name] = in
	}
	histogram := func(name string) coretel.Float64Histogram {
		in := instrument[name]
		return m.Float64Histogram(name, coretel.WithDescription(in.description), coretel.WithUnit(in.unit), coretel.WithBuckets(in.buckets...))
	}
	counter := func(name string) coretel.Int64Counter {
		in := instrument[name]
		return m.Int64Counter(name, coretel.WithDescription(in.description), coretel.WithUnit(in.unit))
	}
	upDown := func(name string) coretel.Int64UpDownCounter {
		in := instrument[name]
		return m.Int64UpDownCounter(name, coretel.WithDescription(in.description), coretel.WithUnit(in.unit))
	}
	histogram(httpRequestDurationMetric).Record(ctx, 0.25, core(httpAttrs)...)
	histogram(httpResponseSizeMetric).Record(ctx, 1234, core(httpAttrs)...)
	upDown(httpActiveRequestsMetric).Add(ctx, 1, core(httpAttrs)...)
	counter(impressionMetric).Add(ctx, 1,
		coretel.String("feature_flag.key", "flag"), coretel.String("feature_flag.result.variant", "on"),
		coretel.String("feature_flag.provider.name", "flagd"), coretel.String("feature_flag.reason", "STATIC"),
		coretel.String("feature_flag.set.id", "set"))
	counter(reasonMetric).Add(ctx, 1,
		coretel.String("feature_flag.provider.name", "flagd"), coretel.String("feature_flag.reason", "STATIC"),
		coretel.String("feature_flag.key", "flag"), coretel.String("feature_flag.set.id", "set"))
	counter(reasonMetric).Add(ctx, 1,
		coretel.String("feature_flag.provider.name", "flagd"), coretel.String("feature_flag.reason", "ERROR"),
		coretel.String("ExceptionTypeKeyName", model.FlagNotFoundErrorCode))
	upDown(syncServerActiveStreamsMetric).Add(ctx, 1, coretel.String("selector", "s"))
	histogram(syncServerStreamDurationMetric).Record(ctx, 90, coretel.String("selector", "s"))

	want, got := collectByName(t, todayReader), collectByName(t, coreReader)
	require.Len(t, got, len(endpointInstruments))
	for _, in := range endpointInstruments {
		w, ok := want[in.name]
		require.True(t, ok, "today records %s", in.name)
		g, ok := got[in.name]
		require.True(t, ok, "the core Meter records %s", in.name)
		metricdatatest.AssertEqual(t, w, g, metricdatatest.IgnoreTimestamp())
		if in.kind == "histogram" {
			bounds := g.Data.(metricdata.Histogram[float64]).DataPoints[0].Bounds
			require.Equal(t, in.buckets, bounds, in.name)
		}
	}
}

// BenchmarkAttributeConversion measures what the core API adds per measurement over the SDK: converting the five
// HTTP server metric attributes.
func BenchmarkAttributeConversion(b *testing.B) {
	attrs := []coretel.Attribute{
		coretel.String("service.name", "flagd"),
		coretel.String("http.route", "/ofrep/v1/evaluate/flags"),
		coretel.String("http.request.method", "POST"),
		coretel.String("http.response.status_code", "200"),
		coretel.String("url.scheme", "http"),
	}
	b.Run("convert", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = convert(attrs)
		}
	})
	b.Run("histogram through the core API", func(b *testing.B) {
		p, err := NewFactory(WithoutGlobals(), WithMetricReader(metric.NewManualReader())).
			Create(context.Background(), settings(nil))
		require.NoError(b, err)
		h := p.Meter("flagd").Float64Histogram(httpRequestDurationMetric)
		ctx := context.Background()
		b.ReportAllocs()
		for b.Loop() {
			h.Record(ctx, 0.1, attrs...)
		}
	})
	b.Run("histogram through the SDK", func(b *testing.B) {
		p, err := NewFactory(WithoutGlobals(), WithMetricReader(metric.NewManualReader())).
			Create(context.Background(), settings(nil))
		require.NoError(b, err)
		r := p.(*Provider).recorder
		kvs := convert(attrs)
		ctx := context.Background()
		b.ReportAllocs()
		for b.Loop() {
			r.HTTPRequestDuration(ctx, 100*time.Millisecond, kvs)
		}
	})
}
