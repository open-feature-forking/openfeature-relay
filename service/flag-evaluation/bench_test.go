package service

import (
	"context"
	"net/http/httptest"
	"testing"

	evaluationV2 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v2/evaluationv2connect"
	evalV2 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v2"
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
func newBenchmarkClient(b *testing.B, tp telemetry.Provider) evaluationV2.ServiceClient {
	b.Helper()

	log := logger.NewLogger(nil, false)
	flagStore, err := store.NewStore(log, []string{spanTestSource})
	require.NoError(b, err)
	eval := evaluation.NewEvaluator(log, flagStore)
	require.NoError(b, eval.SetState(coresync.DataSync{FlagData: spanTestFlags, Source: spanTestSource}))

	svc := NewConnectService(log, eval, flagStore, tp)
	svc.setupHandler(iservice.Configuration{ServiceName: "benchmark"})
	server := httptest.NewServer(svc.server.Handler)
	b.Cleanup(server.Close)
	return evaluationV2.NewServiceClient(server.Client(), server.URL)
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
		_, err := client.ResolveBoolean(context.Background(), connect.NewRequest(&evalV2.ResolveBooleanRequest{FlagKey: "bool-flag"}))
		if err != nil {
			b.Fatal(err)
		}
	}
}

// The benchmarks without a suffix run with the noop telemetry provider, which a distribution without telemetry
// injects; those with OpenTelemetry run with the provider flagd-full injects.

func BenchmarkConnectV2ResolveBoolean(b *testing.B) { benchmarkResolveBoolean(b, telemetry.Noop()) }

func BenchmarkConnectV2ResolveBooleanOpenTelemetry(b *testing.B) {
	benchmarkResolveBoolean(b, openTelemetry(b))
}
