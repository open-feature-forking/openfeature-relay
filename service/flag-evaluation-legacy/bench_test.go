package flagevallegacy

import (
	"context"
	"net/http"
	"testing"

	evaluationV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v1/evaluationv1connect"
	evalV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	iservice "github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	evaluation "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	"github.com/open-feature-forking/openfeature-relay/telemetry/otel/oteltest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
)

// newBenchmarkClient serves the flag evaluation endpoint over a real evaluator, recording through tp.
func newBenchmarkClient(b *testing.B, tp telemetry.Provider) evaluationV1.ServiceClient {
	b.Helper()

	log := logger.NewLogger(nil, false)
	flagStore, err := store.NewStore(log, []string{spanTestSource})
	require.NoError(b, err)
	eval := evaluation.NewEvaluator(log, flagStore)
	require.NoError(b, eval.SetState(coresync.DataSync{FlagData: spanTestFlags, Source: spanTestSource}))

	url := serveComposed(b, eval, flagStore, withoutConnectOptions{tp}, iservice.Configuration{ServiceName: "benchmark"})
	return evaluationV1.NewServiceClient(http.DefaultClient, url)
}

// openTelemetry is the OpenTelemetry telemetry provider flagd-full injects, over an in-memory reader.
func openTelemetry(b *testing.B) telemetry.Provider {
	b.Helper()
	tel := telemetrytest.ProviderFor(metric.NewManualReader())
	b.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	return tel
}

func benchmarkResolveBoolean(b *testing.B, tp telemetry.Provider) {
	client := newBenchmarkClient(b, tp)
	b.ReportAllocs()
	for b.Loop() {
		_, err := client.ResolveBoolean(context.Background(), connect.NewRequest(&evalV1.ResolveBooleanRequest{FlagKey: "bool-flag"}))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkResolveAll(b *testing.B, tp telemetry.Provider) {
	client := newBenchmarkClient(b, tp)
	b.ReportAllocs()
	for b.Loop() {
		_, err := client.ResolveAll(context.Background(), connect.NewRequest(&evalV1.ResolveAllRequest{}))
		if err != nil {
			b.Fatal(err)
		}
	}
}

// The benchmarks without a suffix run with the noop telemetry provider, which a distribution without telemetry
// injects; those with OpenTelemetry run with the provider flagd-full injects.

func BenchmarkConnectResolveBoolean(b *testing.B) { benchmarkResolveBoolean(b, telemetry.Noop()) }

func BenchmarkConnectResolveBooleanOpenTelemetry(b *testing.B) {
	benchmarkResolveBoolean(b, openTelemetry(b))
}

func BenchmarkConnectResolveAll(b *testing.B) { benchmarkResolveAll(b, telemetry.Noop()) }

func BenchmarkConnectResolveAllOpenTelemetry(b *testing.B) { benchmarkResolveAll(b, openTelemetry(b)) }
