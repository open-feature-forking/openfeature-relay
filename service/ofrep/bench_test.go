package ofrep

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	evaluation "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	compressmw "github.com/open-feature-forking/openfeature-relay/service/transport/middleware/compress"
	"github.com/open-feature-forking/openfeature-relay/telemetry/otel/oteltest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
)

// newBenchmarkServer serves the OFREP endpoint over a real evaluator, recording through tp.
func newBenchmarkServer(b *testing.B, tp telemetry.Provider) *httptest.Server {
	b.Helper()

	log := logger.NewLogger(nil, false)
	flagStore, err := store.NewStore(log, []string{spanTestSource})
	require.NoError(b, err)
	eval := evaluation.NewEvaluator(log, flagStore)
	require.NoError(b, eval.SetState(coresync.DataSync{FlagData: spanTestFlags, Source: spanTestSource}))

	compression, err := compressmw.New()
	require.NoError(b, err)
	server := httptest.NewServer(NewOfrepHandler(log, eval, nil, nil, tp, "flagd", SSEConfig{}, compression))
	b.Cleanup(server.Close)
	return server
}

// openTelemetry is the OpenTelemetry telemetry provider flagd-full injects, over an in-memory reader.
func openTelemetry(b *testing.B) telemetry.Provider {
	b.Helper()
	tel := telemetrytest.ProviderFor(metric.NewManualReader())
	b.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	return tel
}

func benchmarkPostWith(b *testing.B, path string, tp telemetry.Provider) {
	server := newBenchmarkServer(b, tp)
	b.ReportAllocs()
	for b.Loop() {
		resp, err := http.Post(server.URL+path, "application/json", strings.NewReader("{}")) //nolint:noctx // benchmark
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("status %d", resp.StatusCode)
		}
	}
}

// The benchmarks without a suffix run with the noop telemetry provider, which a distribution without telemetry
// injects; those with OpenTelemetry run with the provider flagd-full injects.

func BenchmarkOFREPSingle(b *testing.B) {
	benchmarkPostWith(b, "/ofrep/v1/evaluate/flags/bool-flag", telemetry.Noop())
}

func BenchmarkOFREPSingleOpenTelemetry(b *testing.B) {
	benchmarkPostWith(b, "/ofrep/v1/evaluate/flags/bool-flag", openTelemetry(b))
}

func BenchmarkOFREPBulk(b *testing.B) { benchmarkPostWith(b, "/ofrep/v1/evaluate/flags", telemetry.Noop()) }

func BenchmarkOFREPBulkOpenTelemetry(b *testing.B) {
	benchmarkPostWith(b, "/ofrep/v1/evaluate/flags", openTelemetry(b))
}
