package service

import (
	"context"
	"net/http/httptest"
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

	svc := NewConnectService(log, eval, flagStore, tp)
	svc.setupHandler(iservice.Configuration{ServiceName: "benchmark"})
	server := httptest.NewServer(svc.server.Handler)
	b.Cleanup(server.Close)
	return evaluationV1.NewServiceClient(server.Client(), server.URL)
}

// benchmarkTelemetry runs a benchmark with the noop telemetry provider, which a distribution without telemetry
// injects, and with the OpenTelemetry provider, which flagd-full injects.
func benchmarkTelemetry(b *testing.B, run func(b *testing.B, tp telemetry.Provider)) {
	b.Run("noop", func(b *testing.B) { run(b, telemetry.Noop()) })
	b.Run("opentelemetry", func(b *testing.B) {
		tel := telemetrytest.ProviderFor(metric.NewManualReader())
		b.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
		run(b, tel)
	})
}

func BenchmarkConnectResolveBoolean(b *testing.B) {
	benchmarkTelemetry(b, func(b *testing.B, tp telemetry.Provider) {
		client := newBenchmarkClient(b, tp)
		b.ReportAllocs()
		for b.Loop() {
			_, err := client.ResolveBoolean(context.Background(), connect.NewRequest(&evalV1.ResolveBooleanRequest{FlagKey: "bool-flag"}))
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkConnectResolveAll(b *testing.B) {
	benchmarkTelemetry(b, func(b *testing.B, tp telemetry.Provider) {
		client := newBenchmarkClient(b, tp)
		b.ReportAllocs()
		for b.Loop() {
			_, err := client.ResolveAll(context.Background(), connect.NewRequest(&evalV1.ResolveAllRequest{}))
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
