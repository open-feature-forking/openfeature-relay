package flagdgrpc

import (
	"context"
	"errors"
	"net"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"

	syncv1grpc "buf.build/gen/go/open-feature/flagd/grpc/go/flagd/sync/v1/syncv1grpc"
	v1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc/credentials"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// These tests pin what a gRPC source does with today's settings: no `grpc` object, nothing added. A feature that
// adds to the source must leave every one of them unchanged when its setting is absent.

// recordingServer is a flag sync server that records what reaches it.
type recordingServer struct {
	syncv1grpc.UnimplementedFlagSyncServiceServer

	mu            gosync.Mutex
	syncMetadata  metadata.MD
	fetchMetadata metadata.MD
	syncRequest   *v1.SyncFlagsRequest
	fetchRequest  *v1.FetchAllFlagsRequest
	fetchDeadline bool
	syncDeadline  bool
	// syncErr is returned by SyncFlags after it has recorded the call; nil keeps the stream open until it ends
	syncErr error
}

func (s *recordingServer) SyncFlags(req *v1.SyncFlagsRequest, stream syncv1grpc.FlagSyncService_SyncFlagsServer) error {
	s.mu.Lock()
	s.syncMetadata, _ = metadata.FromIncomingContext(stream.Context())
	s.syncRequest = req
	_, s.syncDeadline = stream.Context().Deadline()
	err := s.syncErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := stream.Send(&v1.SyncFlagsResponse{FlagConfiguration: "{}"}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return nil
}

func (s *recordingServer) FetchAllFlags(ctx context.Context, req *v1.FetchAllFlagsRequest) (*v1.FetchAllFlagsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetchMetadata, _ = metadata.FromIncomingContext(ctx)
	s.fetchRequest = req
	_, s.fetchDeadline = ctx.Deadline()
	return &v1.FetchAllFlagsResponse{FlagConfiguration: "{}"}, nil
}

// loopback serves srv on 127.0.0.1 and returns the address.
func loopback(t *testing.T, srv syncv1grpc.FlagSyncServiceServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	syncv1grpc.RegisterFlagSyncServiceServer(server, srv)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)
	return lis.Addr().String()
}

// defaultSync is a Sync as the factory builds it from a source with no settings beyond its URI.
func defaultSync(uri string) *Sync {
	return &Sync{
		Logger:            logger.NewLogger(nil, false),
		URI:               uri,
		CredentialBuilder: &credentials.CredentialBuilder{},
	}
}

// fakeClock stands in for time.After. Every delay fires at once; the one after the stopAfter-th cancels the context.
type fakeClock struct {
	mu        gosync.Mutex
	delays    []time.Duration
	calls     []int // the number of SyncFlags calls made when each delay was asked for
	stopAfter int
	cancel    context.CancelFunc
	callCount func() int
	first     chan struct{}
}

func (f *fakeClock) after(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	f.delays = append(f.delays, d)
	f.calls = append(f.calls, f.callCount())
	n := len(f.delays)
	f.mu.Unlock()
	if n == 1 && f.first != nil {
		close(f.first)
	}
	if n >= f.stopAfter {
		f.cancel()
		return nil
	}
	ch := make(chan time.Time, 1)
	ch <- time.Time{}
	return ch
}

// scriptedSyncClient answers the n-th SyncFlags call with step n-1; later calls fail.
type scriptedSyncClient struct {
	syncv1grpc.FlagSyncServiceClient
	steps []func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error)
	calls atomic.Int32
}

func (c *scriptedSyncClient) SyncFlags(context.Context, *v1.SyncFlagsRequest, ...grpc.CallOption) (syncv1grpc.FlagSyncService_SyncFlagsClient, error) {
	n := int(c.calls.Add(1))
	if n <= len(c.steps) {
		return c.steps[n-1]()
	}
	return nil, errors.New("the source is down")
}

func failOpen(err error) func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error) {
	return func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error) { return nil, err }
}

func payloadThenEnd() (syncv1grpc.FlagSyncService_SyncFlagsClient, error) {
	return &oneShotStream{}, nil
}

// runSync runs s.Sync with the fake clock and returns the clock once the clock stopped it, plus whether Sync
// returned.
func runSync(t *testing.T, s *Sync, client *scriptedSyncClient, stopAfter int) (*fakeClock, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	clock := &fakeClock{stopAfter: stopAfter, cancel: cancel, callCount: func() int { return int(client.calls.Load()) }}
	s.client = client
	s.after = clock.after
	stopped := make(chan error, 1)
	data := make(chan sync.DataSync, 16)
	go func() { stopped <- s.Sync(ctx, data) }()
	t.Cleanup(cancel)
	select {
	case err := <-stopped:
		stopped <- err
	case <-time.After(10 * time.Second):
		t.Fatal("Sync did not stop")
	}
	return clock, stopped
}

func TestDefaultRequestMetadataCharacterization(t *testing.T) {
	srv := &recordingServer{}
	s := defaultSync(loopback(t, srv))
	s.Selector = "source=orders"
	s.ProviderID = "provider-1"
	s.Headers = map[string]string{"x-proxy-gateway-host": "myhost.service", "x-tenant-id": "tenant1"}
	require.NoError(t, s.Init(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	data := make(chan sync.DataSync, 4)
	go func() { _ = s.Sync(ctx, data) }()
	select {
	case <-data:
	case <-time.After(10 * time.Second):
		t.Fatal("no payload from SyncFlags")
	}
	require.NoError(t, s.ReSync(ctx, data))

	srv.mu.Lock()
	defer srv.mu.Unlock()
	for name, md := range map[string]metadata.MD{"SyncFlags": srv.syncMetadata, "FetchAllFlags": srv.fetchMetadata} {
		require.NotContains(t, md, "flagd-selector", name+" sends the selector in the request only")
		require.Equal(t, []string{"myhost.service"}, md.Get("x-proxy-gateway-host"), name)
		require.Equal(t, []string{"tenant1"}, md.Get("x-tenant-id"), name)
	}
	require.Equal(t, "source=orders", srv.syncRequest.GetSelector())  //nolint:staticcheck // deprecated selector kept for backwards compat
	require.Equal(t, "source=orders", srv.fetchRequest.GetSelector()) //nolint:staticcheck // deprecated selector kept for backwards compat
	require.Equal(t, "provider-1", srv.syncRequest.GetProviderId())
}

func TestDefaultReconnectScheduleCharacterization(t *testing.T) {
	t.Run("the delays grow by powers of 4 up to 64 s, then stay at 60 s", func(t *testing.T) {
		client := &scriptedSyncClient{}
		clock, _ := runSync(t, defaultSync("host:8013"), client, 5)
		require.Equal(t, []time.Duration{4 * time.Second, 16 * time.Second, 64 * time.Second, 60 * time.Second, 60 * time.Second}, clock.delays)
	})

	t.Run("the first failure of Sync goes straight to the schedule", func(t *testing.T) {
		client := &scriptedSyncClient{}
		s := defaultSync("host:8013")
		clock, _ := runSync(t, s, client, 1)
		require.Equal(t, []time.Duration{4 * time.Second}, clock.delays)
		require.Equal(t, []int{1}, clock.calls, "one call was made before the first delay")
		require.False(t, s.IsReady())
	})

	t.Run("each delay is followed by exactly one attempt", func(t *testing.T) {
		client := &scriptedSyncClient{}
		clock, _ := runSync(t, defaultSync("host:8013"), client, 4)
		require.Equal(t, []int{1, 2, 3, 4}, clock.calls)
	})

	t.Run("the schedule restarts after a stream ends", func(t *testing.T) {
		client := &scriptedSyncClient{steps: []func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error){
			payloadThenEnd,                  // the initial stream
			failOpen(errors.New("refused")), // the first attempt after the 4 s delay
			payloadThenEnd,                  // the second, after 16 s
		}}
		clock, _ := runSync(t, defaultSync("host:8013"), client, 6)
		// 4 s and 16 s up to the stream that opens, then from the start again once it ended
		require.Equal(t, []time.Duration{
			4 * time.Second, 16 * time.Second,
			4 * time.Second, 16 * time.Second, 64 * time.Second, 60 * time.Second,
		}, clock.delays)
	})
}

func TestDefaultDialOptionsCharacterization(t *testing.T) {
	record := func(s *Sync) *[]grpc.DialOption {
		var got []grpc.DialOption
		s.dialRecorder = func(opts []grpc.DialOption) { got = opts }
		return &got
	}

	t.Run("credentials only: no keepalive, no service config, no interceptor", func(t *testing.T) {
		s := defaultSync("host:8013")
		got := record(s)
		require.NoError(t, s.Init(context.Background()))
		require.Len(t, *got, 1, "the transport credentials")
	})

	t.Run("a maximum message size adds its call option only", func(t *testing.T) {
		s := defaultSync("host:8013")
		s.MaxMsgSize = 1024
		got := record(s)
		require.NoError(t, s.Init(context.Background()))
		require.Len(t, *got, 2, "the transport credentials and the call options")
	})

	t.Run("the options a distribution injects come last and nothing else is added", func(t *testing.T) {
		s := defaultSync("host:8013")
		injected := grpc.WithUserAgent("distribution")
		s.DialOptions = []grpc.DialOption{injected}
		got := record(s)
		require.NoError(t, s.Init(context.Background()))
		require.Len(t, *got, 2)
	})

	t.Run("an override is used as it is, followed by the injected options", func(t *testing.T) {
		s := defaultSync("host:8013")
		s.GrpcDialOptionsOverride = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
		s.DialOptions = []grpc.DialOption{grpc.WithUserAgent("distribution")}
		got := record(s)
		require.NoError(t, s.Init(context.Background()))
		require.Len(t, *got, 2)
	})

	t.Run("no call carries a deadline of its own", func(t *testing.T) {
		srv := &recordingServer{}
		s := defaultSync(loopback(t, srv))
		require.NoError(t, s.Init(context.Background()))
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		data := make(chan sync.DataSync, 4)
		go func() { _ = s.Sync(ctx, data) }()
		select {
		case <-data:
		case <-time.After(10 * time.Second):
			t.Fatal("no payload from SyncFlags")
		}
		require.NoError(t, s.ReSync(ctx, data))

		srv.mu.Lock()
		defer srv.mu.Unlock()
		require.False(t, srv.fetchDeadline, "FetchAllFlags has no deadline")
		require.False(t, srv.syncDeadline, "SyncFlags has no deadline")
	})
}

func TestDefaultFailFastCharacterization(t *testing.T) {
	t.Run("a closed port fails at once and enters the schedule", func(t *testing.T) {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := lis.Addr().String()
		require.NoError(t, lis.Close())

		s := defaultSync(addr)
		require.NoError(t, s.Init(context.Background()))
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		first := make(chan struct{})
		clock := &fakeClock{stopAfter: 1, cancel: cancel, callCount: func() int { return 1 }, first: first}
		s.after = clock.after
		stopped := make(chan error, 1)
		start := time.Now()
		go func() { stopped <- s.Sync(ctx, make(chan sync.DataSync, 1)) }()
		select {
		case <-first:
			require.Less(t, time.Since(start), 5*time.Second, "without WaitForReady the call does not wait for the server")
		case <-time.After(20 * time.Second):
			t.Fatal("SyncFlags waited for the connection instead of failing")
		}
		require.NoError(t, <-stopped)
		require.Equal(t, []time.Duration{4 * time.Second}, clock.delays)
	})

	t.Run("PERMISSION_DENIED on the first call is retried like any error", func(t *testing.T) {
		denied := status.Error(codes.PermissionDenied, "no")
		client := &scriptedSyncClient{steps: []func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error){failOpen(denied), failOpen(denied)}}
		clock, stopped := runSync(t, defaultSync("host:8013"), client, 3)
		require.Equal(t, []time.Duration{4 * time.Second, 16 * time.Second, 64 * time.Second}, clock.delays)
		require.Equal(t, []int{1, 2, 3}, clock.calls, "every delay is followed by another attempt")
		require.NoError(t, <-stopped, "flagd keeps running until it is stopped")
	})

	t.Run("PERMISSION_DENIED on the first Recv is retried like any error", func(t *testing.T) {
		srv := &recordingServer{syncErr: status.Error(codes.PermissionDenied, "no")}
		s := defaultSync(loopback(t, srv))
		require.NoError(t, s.Init(context.Background()))
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		clock := &fakeClock{stopAfter: 3, cancel: cancel, callCount: func() int { return 0 }}
		s.after = clock.after
		stopped := make(chan error, 1)
		go func() { stopped <- s.Sync(ctx, make(chan sync.DataSync, 1)) }()
		select {
		case err := <-stopped:
			require.NoError(t, err, "flagd keeps running until it is stopped")
		case <-time.After(20 * time.Second):
			t.Fatal("Sync did not stop")
		}
		// the stream opens each time and only then fails, so every failure starts the schedule again
		require.Equal(t, []time.Duration{4 * time.Second, 4 * time.Second, 4 * time.Second}, clock.delays)
	})
}
