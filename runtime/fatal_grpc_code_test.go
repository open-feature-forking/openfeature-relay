package runtime

import (
	"context"
	"net"
	"testing"
	"time"

	"buf.build/gen/go/open-feature/flagd/grpc/go/flagd/sync/v1/syncv1grpc"
	v1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	flagdgrpc "github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// deniedServer answers every stream with PERMISSION_DENIED, as an authenticating proxy does for a wrong token.
type deniedServer struct {
	syncv1grpc.UnimplementedFlagSyncServiceServer
}

func (deniedServer) SyncFlags(*v1.SyncFlagsRequest, syncv1grpc.FlagSyncService_SyncFlagsServer) error {
	return status.Error(codes.PermissionDenied, "wrong token")
}

// A distribution whose gRPC source lists PERMISSION_DENIED as fatal does not keep serving when the source denies it
// before its first payload: the runtime returns an error that names the source and the code, and the process exits
// non-zero.
func TestRuntimeExitsOnFatalGRPCCode(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	syncv1grpc.RegisterFlagSyncServiceServer(server, deniedServer{})
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)
	source := lis.Addr().String()

	log := logger.NewLogger(nil, false)
	provider, err := flagdgrpc.Factory{}.CreateStreaming(coresync.Settings{
		Source: coresync.SourceConfig{
			URI:      source,
			Provider: flagdgrpc.Provider,
			GRPC:     &coresync.GRPCSourceSettings{FatalStatusCodes: []string{"PERMISSION_DENIED"}},
		},
		Logger: log,
	})
	require.NoError(t, err)

	rt := &Runtime{
		Logger:          log,
		DefaultFormat:   nopFormat{},
		Syncs:           []coresync.ISync{provider},
		Endpoints:       []NamedEndpoint{{Type: "blocking", Endpoint: blockingEndpoint{stop: make(chan struct{})}}},
		sourceReadiness: map[string]bool{source: false},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- rt.start(ctx) }()

	select {
	case err := <-done:
		require.ErrorContains(t, err, source)
		require.ErrorContains(t, err, "PERMISSION_DENIED")
	case <-time.After(10 * time.Second):
		t.Fatal("the runtime kept running with a source that denies it")
	}
}
