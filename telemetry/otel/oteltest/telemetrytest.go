// Package telemetrytest provides an OpenTelemetry telemetry provider that keeps its spans and metrics in memory,
// for the tests of flagd's service endpoints and runtime. It installs no global state.
package telemetrytest

import (
	"context"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	otelprovider "github.com/open-feature-forking/openfeature-relay/telemetry/otel"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace"
)

// Recorder is a telemetry provider that keeps what it records.
type Recorder struct {
	telemetry.Provider
	// Metrics reads the metrics the provider recorded.
	Metrics *metric.ManualReader

	flush func(context.Context) error
	spans *exporter
}

// New creates a Recorder whose metric views are those of a flagd distribution named "flagd".
func New() *Recorder {
	r := &Recorder{Metrics: metric.NewManualReader(), spans: &exporter{}}
	p, err := otelprovider.NewFactory(
		otelprovider.WithoutGlobals(), otelprovider.WithSpanExporter(r.spans), otelprovider.WithMetricReader(r.Metrics),
	).Create(context.Background(), telemetry.Settings{DistributionName: "flagd", DistributionVersion: "test"})
	if err != nil {
		panic(err)
	}
	r.Provider = p
	r.flush = p.(*otelprovider.Provider).ForceFlush
	return r
}

// ConnectHandlerOptions instrument a ConnectRPC handler with the provider, as FromConfig does.
func (r *Recorder) ConnectHandlerOptions() []connect.HandlerOption {
	return r.Provider.(*otelprovider.Provider).ConnectHandlerOptions()
}

// ProviderFor returns a telemetry provider that records its metrics to reader and drops its spans.
func ProviderFor(reader metric.Reader) telemetry.Provider {
	p, err := otelprovider.NewFactory(
		otelprovider.WithoutGlobals(), otelprovider.WithSpanExporter(discardExporter{}), otelprovider.WithMetricReader(reader),
	).
		Create(context.Background(), telemetry.Settings{DistributionName: "flagd", DistributionVersion: "test"})
	if err != nil {
		panic(err)
	}
	return p
}

// Ended returns the spans that ended, in the order they ended.
func (r *Recorder) Ended() []trace.ReadOnlySpan {
	_ = r.flush(context.Background())
	return r.spans.snapshot()
}

// Reset forgets the spans that ended.
func (r *Recorder) Reset() {
	_ = r.flush(context.Background())
	r.spans.reset()
}

// Collect returns the metrics recorded so far.
func (r *Recorder) Collect(ctx context.Context) (metricdata.ResourceMetrics, error) {
	var data metricdata.ResourceMetrics
	err := r.Metrics.Collect(ctx, &data)
	return data, err
}

// Evaluation is an evaluation as the evaluation metrics record it: a successful one with its flag key and variant,
// a failed one with the text of its error and without either.
type Evaluation struct {
	ErrorText string
	Reason    string
	Variant   string
	FlagKey   string
	FlagSetID string
}

// Evaluations returns the evaluations recorded in the metrics of reader, in no particular order.
func Evaluations(t testing.TB, reader metric.Reader) []Evaluation {
	t.Helper()

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &data))

	var out []Evaluation
	for _, sm := range data.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, point := range sum.DataPoints {
				attr := func(key string) string {
					v, _ := point.Attributes.Value(attribute.Key(key))
					return v.AsString()
				}
				var e Evaluation
				switch m.Name {
				case "feature_flag.flagd.impression":
					e = Evaluation{Reason: attr("feature_flag.reason"), Variant: attr("feature_flag.result.variant"),
						FlagKey: attr("feature_flag.key"), FlagSetID: attr("feature_flag.set.id")}
				case "feature_flag.flagd.result.reason":
					if !point.Attributes.HasValue("ExceptionTypeKeyName") {
						continue
					}
					e = Evaluation{ErrorText: attr("ExceptionTypeKeyName"), Reason: attr("feature_flag.reason"),
						FlagSetID: attr("feature_flag.set.id")}
				default:
					continue
				}
				for range point.Value {
					out = append(out, e)
				}
			}
		}
	}
	return out
}

// RequestDurationPoints returns the scopes that recorded http.server.request.duration, and its data points.
func RequestDurationPoints(
	t testing.TB, reader metric.Reader,
) (map[string]bool, []metricdata.HistogramDataPoint[float64]) {
	t.Helper()

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &data))

	scopes := map[string]bool{}
	var points []metricdata.HistogramDataPoint[float64]
	for _, sm := range data.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "http.server.request.duration" {
				scopes[sm.Scope.Name] = true
				points = append(points, m.Data.(metricdata.Histogram[float64]).DataPoints...)
			}
		}
	}
	return scopes, points
}

// discardExporter drops the spans it gets.
type discardExporter struct{}

func (discardExporter) ExportSpans(context.Context, []trace.ReadOnlySpan) error { return nil }

func (discardExporter) Shutdown(context.Context) error { return nil }

// exporter keeps the spans it gets. Unlike the in-memory exporter of the SDK it keeps them after Shutdown.
type exporter struct {
	mu    sync.Mutex
	spans []trace.ReadOnlySpan
}

func (e *exporter) ExportSpans(_ context.Context, spans []trace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (e *exporter) Shutdown(context.Context) error { return nil }

func (e *exporter) snapshot() []trace.ReadOnlySpan {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]trace.ReadOnlySpan(nil), e.spans...)
}

func (e *exporter) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = nil
}
