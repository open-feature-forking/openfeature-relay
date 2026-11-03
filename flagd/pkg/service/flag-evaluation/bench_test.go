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
	"github.com/open-feature/flagd/flagd/pkg/evaluation"
	"github.com/stretchr/testify/require"
)

// newBenchmarkClient serves the flag evaluation endpoint over a real evaluator with today's noop metrics
// recorder and the noop telemetry provider.
func newBenchmarkClient(b *testing.B) evaluationV1.ServiceClient {
	b.Helper()

	log := logger.NewLogger(nil, false)
	flagStore, err := store.NewStore(log, []string{spanTestSource})
	require.NoError(b, err)
	eval := evaluation.NewEvaluator(log, flagStore)
	require.NoError(b, eval.SetState(coresync.DataSync{FlagData: spanTestFlags, Source: spanTestSource}))

	svc := NewConnectService(log, eval, flagStore, nil)
	svc.setupHandler(iservice.Configuration{ServiceName: "benchmark"})
	server := httptest.NewServer(svc.server.Handler)
	b.Cleanup(server.Close)
	return evaluationV1.NewServiceClient(server.Client(), server.URL)
}

func BenchmarkConnectResolveBoolean(b *testing.B) {
	client := newBenchmarkClient(b)
	b.ReportAllocs()
	for b.Loop() {
		_, err := client.ResolveBoolean(context.Background(), connect.NewRequest(&evalV1.ResolveBooleanRequest{FlagKey: "bool-flag"}))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConnectResolveAll(b *testing.B) {
	client := newBenchmarkClient(b)
	b.ReportAllocs()
	for b.Loop() {
		_, err := client.ResolveAll(context.Background(), connect.NewRequest(&evalV1.ResolveAllRequest{}))
		if err != nil {
			b.Fatal(err)
		}
	}
}
