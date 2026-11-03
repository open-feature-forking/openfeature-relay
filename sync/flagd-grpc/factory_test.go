package flagdgrpc

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"buf.build/gen/go/open-feature/flagd/grpc/go/flagd/sync/v1/syncv1grpc"
	v1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

const (
	flagsOne = `{"flags":{"one":{}}}`
	flagsTwo = `{"flags":{"two":{}}}`
)

// pushServer is a flagd sync service on an in-memory listener. It can be stopped and started again.
type pushServer struct {
	syncv1grpc.UnimplementedFlagSyncServiceServer

	current  atomic.Value // the flag configuration
	listener atomic.Pointer[bufconn.Listener]
	server   *grpc.Server
	changes  chan string
	streams  chan struct{}
}

func newPushServer() *pushServer {
	s := &pushServer{changes: make(chan string, 8), streams: make(chan struct{}, 8)}
	s.current.Store(flagsOne)
	return s
}

func (s *pushServer) start() {
	listener := bufconn.Listen(1 << 16)
	s.listener.Store(listener)
	s.server = grpc.NewServer()
	syncv1grpc.RegisterFlagSyncServiceServer(s.server, s)
	go func() { _ = s.server.Serve(listener) }()
}

func (s *pushServer) stop() { s.server.Stop() }

func (s *pushServer) SyncFlags(_ *v1.SyncFlagsRequest, stream syncv1grpc.FlagSyncService_SyncFlagsServer) error {
	if err := stream.Send(&v1.SyncFlagsResponse{FlagConfiguration: s.current.Load().(string)}); err != nil {
		return err
	}
	for {
		select {
		case flags := <-s.changes:
			s.current.Store(flags)
			if err := stream.Send(&v1.SyncFlagsResponse{FlagConfiguration: flags}); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return nil
		}
	}
}

func (s *pushServer) FetchAllFlags(context.Context, *v1.FetchAllFlagsRequest) (*v1.FetchAllFlagsResponse, error) {
	return &v1.FetchAllFlagsResponse{FlagConfiguration: s.current.Load().(string)}, nil
}

func TestStreamingContract(t *testing.T) {
	server := newPushServer()
	server.start()
	t.Cleanup(server.stop)

	f := Factory{DialOptions: []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return server.listener.Load().DialContext(ctx)
		}),
	}}
	synctest.RunStreaming(t, f, synctest.StreamingInputs{
		Source:  sync.SourceConfig{URI: "passthrough:///flagd", Provider: Provider},
		Expect:  flagsOne,
		Change:  func(testing.TB) { server.changes <- flagsTwo },
		Break:   func(testing.TB) { server.stop() },
		Restore: func(testing.TB) { server.start() },
		// the flagd sync protocol has no way to remove a source's flag configuration
		Remove:      func(testing.TB) {},
		Unavailable: func(testing.TB) { server.stop() },
		Available:   func(testing.TB) { server.start() },
		OnRemove:    synctest.Keep,
		Timeout:     6 * time.Second,
	})
}

func TestFactory(t *testing.T) {
	f := Factory{}
	require.Equal(t, "grpc", f.Type())
	require.Equal(t, []string{"grpc://", "grpcs://", "envoy", "dns", "uds", "xds"}, f.Schemes())

	tests := []struct {
		uri  string
		want sync.SourceConfig
	}{
		{"grpc://host:8013", sync.SourceConfig{URI: "host:8013", Provider: "grpc"}},
		{"grpcs://host:8013", sync.SourceConfig{URI: "host:8013", Provider: "grpc", TLS: true}},
		{"envoy://host/svc", sync.SourceConfig{URI: "envoy://host/svc", Provider: "grpc"}},
		{"dns:///host:8013", sync.SourceConfig{URI: "dns:///host:8013", Provider: "grpc"}},
		{"uds:///tmp/sock", sync.SourceConfig{URI: "uds:///tmp/sock", Provider: "grpc"}},
		{"xds://host/svc", sync.SourceConfig{URI: "xds://host/svc", Provider: "grpc"}},
	}
	for _, tt := range tests {
		got, ok := f.ParseURI(tt.uri)
		require.True(t, ok, tt.uri)
		require.Equal(t, tt.want, got, tt.uri)
	}
	_, ok := f.ParseURI("http://host")
	require.False(t, ok)
}

func TestFactoryInjectsWhatTheDistributionProvides(t *testing.T) {
	recorder := &closedStreams{}
	metrics := &receivedRecorder{}
	dial := grpc.WithUserAgent("test")
	provider, err := Factory{DialOptions: []grpc.DialOption{dial}, StreamMetrics: recorder}.CreateStreaming(sync.Settings{
		Source: sync.SourceConfig{
			URI: "host:8013", TLS: true, CertPath: "/ca.pem", ProviderID: "app", Selector: "source=a", MaxMsgSize: 10,
			IncrementalUpdates: true, Headers: map[string]string{"X-Key": "v"},
		},
		Logger:  logger.NewLogger(nil, false),
		Metrics: metrics,
	})
	require.NoError(t, err)

	s := provider.(*Sync)
	require.Equal(t, "host:8013", s.URI)
	require.True(t, s.Secure)
	require.Equal(t, "/ca.pem", s.CertPath)
	require.Equal(t, "app", s.ProviderID)
	require.Equal(t, "source=a", s.Selector)
	require.Equal(t, 10, s.MaxMsgSize)
	require.True(t, s.IncrementalUpdates)
	require.Equal(t, map[string]string{"X-Key": "v"}, s.Headers)
	require.Equal(t, 1, len(s.DialOptions))
	require.Same(t, recorder, s.StreamMetricsRecorder)
	require.Same(t, metrics, s.SyncMetricsRecorder)
	require.NotNil(t, s.CredentialBuilder)
}

type receivedRecorder struct{}

func (*receivedRecorder) RecordFlagConfigReceived(context.Context, string, string, string) {}
