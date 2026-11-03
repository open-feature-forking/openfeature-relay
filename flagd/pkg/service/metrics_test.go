package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature/flagd/flagd/internal/telemetrytest"
	"github.com/open-feature/flagd/flagd/pkg/service"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestMetricsInstruments pins the names, units and histogram buckets the service endpoints have always recorded.
func TestMetricsInstruments(t *testing.T) {
	tel := telemetrytest.New()
	m := service.NewMetrics(tel.Meter(service.MetricsScope))
	ctx := context.Background()
	attrs := service.HTTPAttributes("flagd", "/route", "propfind", "200", "http")
	m.HTTPRequestDuration(ctx, time.Second, attrs)
	m.HTTPResponseSize(ctx, 1234, attrs)
	m.InFlightRequestStart(ctx, attrs)
	m.RecordEvaluation(ctx, nil, "STATIC", "on", "flag", model.Metadata{"flagSetId": "set"})
	m.SyncServerStreamStart(ctx, attrs)
	m.SyncServerStreamDuration(ctx, time.Minute, attrs)

	data, err := tel.Collect(ctx)
	require.NoError(t, err)
	got := map[string]metricdata.Metrics{}
	for _, sm := range data.ScopeMetrics {
		require.Equal(t, service.MetricsScope, sm.Scope.Name)
		for _, metric := range sm.Metrics {
			got[metric.Name] = metric
		}
	}

	units := map[string]string{
		"http.server.request.duration":                   "s",
		"http.server.response.body.size":                 "By",
		"http.server.active_requests":                    "{request}",
		"feature_flag.flagd.impression":                  "{impression}",
		"feature_flag.flagd.result.reason":               "{reason}",
		"feature_flag.flagd.sync.server.active_streams":  "{stream}",
		"feature_flag.flagd.sync.server.stream.duration": "s",
	}
	require.Len(t, got, len(units))
	for name, unit := range units {
		require.Equal(t, unit, got[name].Unit, name)
	}

	buckets := map[string][]float64{
		"http.server.request.duration":                   prometheus.DefBuckets,
		"http.server.response.body.size":                 prometheus.ExponentialBuckets(100, 10, 8),
		"feature_flag.flagd.sync.server.stream.duration": {30, 60, 120, 300, 480, 600, 1200, 1800, 3600, 10800},
	}
	for name, want := range buckets {
		points := got[name].Data.(metricdata.Histogram[float64]).DataPoints
		require.Len(t, points, 1, name)
		require.Equal(t, want, points[0].Bounds, name)
	}

	method, _ := got["http.server.request.duration"].Data.(metricdata.Histogram[float64]).DataPoints[0].Attributes.Value("http.request.method")
	require.Equal(t, "_OTHER", method.AsString(), "an unknown method is a bounded label value")
}
