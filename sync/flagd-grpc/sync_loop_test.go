package flagdgrpc

import (
	"context"
	"errors"
	"io"
	"slices"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"

	syncv1grpc "buf.build/gen/go/open-feature/flagd/grpc/go/flagd/sync/v1/syncv1grpc"
	v1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	permissionDenied = status.Error(codes.PermissionDenied, "no")
	fatalOnDenied    = []codes.Code{codes.PermissionDenied}
)

// optionsClient records the call options of every SyncFlags call.
type optionsClient struct {
	scriptedSyncClient
	mu   gosync.Mutex
	opts [][]grpc.CallOption
}

func (c *optionsClient) SyncFlags(ctx context.Context, in *v1.SyncFlagsRequest, opts ...grpc.CallOption) (syncv1grpc.FlagSyncService_SyncFlagsClient, error) {
	c.mu.Lock()
	c.opts = append(c.opts, opts)
	c.mu.Unlock()
	return c.scriptedSyncClient.SyncFlags(ctx, in, opts...)
}

// deniedAfterPayload delivers one payload and then fails with PERMISSION_DENIED.
type deniedAfterPayload struct {
	syncv1grpc.FlagSyncService_SyncFlagsClient
	sent bool
}

func (s *deniedAfterPayload) Recv() (*v1.SyncFlagsResponse, error) {
	if !s.sent {
		s.sent = true
		return &v1.SyncFlagsResponse{FlagConfiguration: "{}"}, nil
	}
	return nil, permissionDenied
}

// deniedAtOnce opens and fails its first Recv with PERMISSION_DENIED.
type deniedAtOnce struct {
	syncv1grpc.FlagSyncService_SyncFlagsClient
}

func (deniedAtOnce) Recv() (*v1.SyncFlagsResponse, error) { return nil, permissionDenied }

// gatedStream blocks Recv until release is closed, then delivers one payload and blocks until done is closed.
type gatedStream struct {
	syncv1grpc.FlagSyncService_SyncFlagsClient
	release, done chan struct{}
	sent          bool
}

func (s *gatedStream) Recv() (*v1.SyncFlagsResponse, error) {
	if !s.sent {
		select {
		case <-s.release:
			s.sent = true
			return &v1.SyncFlagsResponse{FlagConfiguration: "{}"}, nil
		case <-s.done:
			return nil, io.EOF
		}
	}
	<-s.done
	return nil, io.EOF
}

func TestRetryDelayFixedOnceOptedIn(t *testing.T) {
	t.Run("after an error", func(t *testing.T) {
		s := defaultSync("host:8013")
		s.Options = Options{RetryBackoff: time.Second, RetryBackoffMax: 7 * time.Second}
		clock, _ := runSync(t, s, &scriptedSyncClient{}, 5)
		require.Equal(t, []time.Duration{7 * time.Second, 7 * time.Second, 7 * time.Second, 7 * time.Second, 7 * time.Second}, clock.delays)
	})

	t.Run("after a stream that completed", func(t *testing.T) {
		s := defaultSync("host:8013")
		s.Options = Options{RetryBackoffMax: 7 * time.Second}
		client := &scriptedSyncClient{steps: []func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error){payloadThenEnd, payloadThenEnd}}
		clock, _ := runSync(t, s, client, 3)
		require.Equal(t, []time.Duration{7 * time.Second, 7 * time.Second, 7 * time.Second}, clock.delays,
			"the 4 s, 16 s and 64 s schedule is not used")
	})
}

func TestSingleBackoffKeyFillsTheOther(t *testing.T) {
	for name, tc := range map[string]struct {
		options Options
		want    time.Duration
	}{
		"only retryBackoffMs":    {Options{RetryBackoff: 2 * time.Second}, 5 * time.Second},
		"only retryBackoffMaxMs": {Options{RetryBackoffMax: 9 * time.Second}, 9 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			s := defaultSync("host:8013")
			s.Options = tc.options
			clock, _ := runSync(t, s, &scriptedSyncClient{}, 2)
			require.Equal(t, []time.Duration{tc.want, tc.want}, clock.delays)
		})
	}
}

func TestWaitForReadyOnlyWithRetryPolicy(t *testing.T) {
	for name, tc := range map[string]struct {
		options Options
		want    []grpc.CallOption
	}{
		"no settings":          {Options{}, nil},
		"a stream deadline":    {Options{StreamDeadline: time.Minute}, nil},
		"fatal codes":          {Options{FatalStatusCodes: fatalOnDenied}, nil},
		"retryBackoffMs":       {Options{RetryBackoff: time.Second}, []grpc.CallOption{grpc.WaitForReady(true)}},
		"retryBackoffMaxMs":    {Options{RetryBackoffMax: time.Second}, []grpc.CallOption{grpc.WaitForReady(true)}},
		"the retry policy too": {Options{RetryBackoff: time.Second, StreamDeadline: time.Minute}, []grpc.CallOption{grpc.WaitForReady(true)}},
	} {
		t.Run(name, func(t *testing.T) {
			s := defaultSync("host:8013")
			s.Options = tc.options
			client := &optionsClient{}
			runSyncWith(t, s, client, &client.scriptedSyncClient, 2)
			client.mu.Lock()
			defer client.mu.Unlock()
			require.GreaterOrEqual(t, len(client.opts), 2, "the first call and a retry")
			for _, opts := range client.opts {
				require.Equal(t, tc.want, opts)
			}
		})
	}
}

// runSyncWith is runSync for a client that wraps a scriptedSyncClient.
func runSyncWith(t *testing.T, s *Sync, client syncv1grpc.FlagSyncServiceClient, scripted *scriptedSyncClient, stopAfter int) *fakeClock {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	clock := &fakeClock{stopAfter: stopAfter, cancel: cancel, callCount: func() int { return int(scripted.calls.Load()) }}
	s.client = client
	s.after = clock.after
	stopped := make(chan error, 1)
	go func() { stopped <- s.Sync(ctx, make(chan sync.DataSync, 16)) }()
	t.Cleanup(cancel)
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Sync did not stop")
	}
	return clock
}

func TestFatalCodeOnFirstRecvStopsSync(t *testing.T) {
	t.Run("the stream opens, then the server answers PERMISSION_DENIED", func(t *testing.T) {
		srv := &recordingServer{syncErr: permissionDenied}
		uri := loopback(t, srv)
		s := defaultSync(uri)
		s.Options = Options{FatalStatusCodes: fatalOnDenied}
		require.NoError(t, s.Init(context.Background()))

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := s.Sync(ctx, make(chan sync.DataSync, 1))
		require.Error(t, err)
		require.ErrorContains(t, err, uri, "the error names the source")
		require.ErrorContains(t, err, "PermissionDenied", "and the code")
		require.Equal(t, codes.PermissionDenied, status.Code(err))
		require.True(t, s.IsReady(), "the stream had opened: ready is not the first-payload flag")
	})

	t.Run("the call itself fails", func(t *testing.T) {
		s := defaultSync("host:8013")
		s.Options = Options{FatalStatusCodes: fatalOnDenied}
		client := &scriptedSyncClient{steps: []func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error){failOpen(permissionDenied)}}
		s.client = client
		err := s.Sync(context.Background(), make(chan sync.DataSync, 1))
		require.ErrorContains(t, err, "host:8013")
		require.ErrorContains(t, err, "PermissionDenied")
		require.EqualValues(t, 1, client.calls.Load(), "nothing is retried")
	})

	t.Run("a retry that is denied ends it too", func(t *testing.T) {
		s := defaultSync("host:8013")
		s.Options = Options{FatalStatusCodes: fatalOnDenied}
		client := &scriptedSyncClient{steps: []func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error){failOpen(errors.New("refused")), failOpen(permissionDenied)}}
		s.client = client
		s.after = func(time.Duration) <-chan time.Time { c := make(chan time.Time, 1); c <- time.Time{}; return c }
		err := s.Sync(context.Background(), make(chan sync.DataSync, 1))
		require.ErrorContains(t, err, "PermissionDenied")
		require.EqualValues(t, 2, client.calls.Load())
	})

	t.Run("a code that is not listed is retried", func(t *testing.T) {
		s := defaultSync("host:8013")
		s.Options = Options{FatalStatusCodes: []codes.Code{codes.Unauthenticated}}
		clock, stopped := runSync(t, s, &scriptedSyncClient{steps: []func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error){failOpen(permissionDenied)}}, 2)
		require.Len(t, clock.delays, 2)
		require.NoError(t, <-stopped)
	})
}

func TestFatalCodeAfterFirstPayloadIsRetried(t *testing.T) {
	s := defaultSync("host:8013")
	s.Options = Options{FatalStatusCodes: fatalOnDenied}
	deniedLater := func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error) { return &deniedAfterPayload{}, nil }
	client := &scriptedSyncClient{steps: []func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error){deniedLater, failOpen(permissionDenied)}}
	clock, stopped := runSync(t, s, client, 3)
	require.Len(t, clock.delays, 3, "PERMISSION_DENIED after the first payload is retried, on the stream and on a new call")
	require.NoError(t, <-stopped)
}

func TestFatalCodesPerSync(t *testing.T) {
	denied := func() *scriptedSyncClient {
		return &scriptedSyncClient{steps: []func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error){failOpen(permissionDenied)}}
	}
	listed, other := defaultSync("listed:8013"), defaultSync("other:8013")
	listed.Options = Options{FatalStatusCodes: fatalOnDenied}
	other.Options = Options{FatalStatusCodes: []codes.Code{codes.Unauthenticated}}

	listed.client = denied()
	require.ErrorContains(t, listed.Sync(context.Background(), make(chan sync.DataSync, 1)), "listed:8013")

	_, stopped := runSync(t, other, denied(), 2)
	require.NoError(t, <-stopped, "another source's set does not end this one")
}

func TestStreamDeadlineRecyclesWithoutDelay(t *testing.T) {
	var opened atomic.Int32
	srv := &countingServer{opened: &opened}
	s := defaultSync(loopback(t, srv))
	streams := &countedStreams{}
	s.StreamMetricsRecorder = streams
	s.Options = Options{StreamDeadline: 150 * time.Millisecond}
	require.NoError(t, s.Init(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := &fakeClock{stopAfter: 1000, cancel: cancel, callCount: func() int { return 0 }}
	s.after = clock.after
	stopped := make(chan error, 1)
	go func() { stopped <- s.Sync(ctx, make(chan sync.DataSync, 64)) }()

	require.Eventually(t, func() bool { return opened.Load() >= 4 }, 10*time.Second, 20*time.Millisecond,
		"the stream is opened again each time its deadline passes")
	clock.mu.Lock()
	delays := slices.Clone(clock.delays)
	clock.mu.Unlock()
	reconnects := streams.reconnects.Load()
	cancel()
	require.NoError(t, <-stopped)

	require.Empty(t, delays, "no delay between a deadline and the next stream")
	require.Zero(t, reconnects, "a recycle is not a reconnect")
}

// countingServer sends one payload on every stream and keeps the stream open.
type countingServer struct {
	syncv1grpc.UnimplementedFlagSyncServiceServer
	opened *atomic.Int32
}

func (s *countingServer) SyncFlags(_ *v1.SyncFlagsRequest, stream syncv1grpc.FlagSyncService_SyncFlagsServer) error {
	s.opened.Add(1)
	if err := stream.Send(&v1.SyncFlagsResponse{FlagConfiguration: "{}"}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return nil
}

type countedStreams struct{ reconnects atomic.Int32 }

func (*countedStreams) RecordStreamOpened(context.Context, string, string) {}
func (*countedStreams) RecordStreamClosed(context.Context, string, string) {}
func (c *countedStreams) RecordReconnect(context.Context, string, string)  { c.reconnects.Add(1) }

func TestStreamWithoutDeadlineIsNotRecycled(t *testing.T) {
	var opened atomic.Int32
	s := defaultSync(loopback(t, &countingServer{opened: &opened}))
	require.NoError(t, s.Init(context.Background()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Sync(ctx, make(chan sync.DataSync, 64)) }()
	require.Eventually(t, func() bool { return opened.Load() == 1 }, 10*time.Second, 10*time.Millisecond)
	time.Sleep(500 * time.Millisecond)
	require.EqualValues(t, 1, opened.Load())
}

func syncInBackground(t *testing.T, s *Sync, data chan sync.DataSync) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { defer close(stopped); _ = s.Sync(ctx, data) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("Sync did not stop")
		}
	})
}

func TestReadyWhenStreamOpensByDefault(t *testing.T) {
	stream := &gatedStream{release: make(chan struct{}), done: make(chan struct{})}
	s := defaultSync("host:8013")
	s.client = &scriptedSyncClient{steps: []func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error){
		func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error) { return stream, nil },
	}}
	syncInBackground(t, s, make(chan sync.DataSync, 4))
	t.Cleanup(func() { close(stream.done) }) // runs before the stop of Sync, which waits for the stream
	require.Eventually(t, s.IsReady, 5*time.Second, 5*time.Millisecond, "ready with no payload yet")
}

func TestReadyOnFirstPayloadOptIn(t *testing.T) {
	t.Run("ready only once the first payload has arrived", func(t *testing.T) {
		stream := &gatedStream{release: make(chan struct{}), done: make(chan struct{})}
		client := &scriptedSyncClient{steps: []func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error){
			func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error) { return stream, nil },
		}}
		s := defaultSync("host:8013")
		s.Options = Options{ReadyOnFirstPayload: true}
		s.client = client
		syncInBackground(t, s, make(chan sync.DataSync, 4))
		t.Cleanup(func() { close(stream.done) })

		require.Eventually(t, func() bool { return client.calls.Load() == 1 }, 5*time.Second, 5*time.Millisecond)
		time.Sleep(200 * time.Millisecond)
		require.False(t, s.IsReady(), "the stream is open and no payload arrived")
		close(stream.release)
		require.Eventually(t, s.IsReady, 5*time.Second, 5*time.Millisecond)
	})

	t.Run("a stream that opens and fails before any payload is never ready", func(t *testing.T) {
		s := defaultSync("host:8013")
		s.Options = Options{ReadyOnFirstPayload: true}
		opensThenFails := func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error) { return deniedAtOnce{}, nil }
		client := &scriptedSyncClient{steps: []func() (syncv1grpc.FlagSyncService_SyncFlagsClient, error){opensThenFails, opensThenFails, opensThenFails}}
		_, stopped := runSync(t, s, client, 3)
		require.NoError(t, <-stopped)
		require.False(t, s.IsReady())
	})
}
