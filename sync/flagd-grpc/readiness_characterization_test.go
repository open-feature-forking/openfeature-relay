package flagdgrpc

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	syncv1grpc "buf.build/gen/go/open-feature/flagd/grpc/go/flagd/sync/v1/syncv1grpc"
	v1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// scriptedClient opens one stream that delivers a payload and then ends, and refuses every later stream.
type scriptedClient struct {
	syncv1grpc.FlagSyncServiceClient
	firstErr error
	// recovers makes the second call open a stream, after a first call that failed
	recovers bool
	calls    atomic.Int32
}

type oneShotStream struct {
	syncv1grpc.FlagSyncService_SyncFlagsClient
	sent bool
}

func (s *oneShotStream) Recv() (*v1.SyncFlagsResponse, error) {
	if s.sent {
		return nil, io.EOF
	}
	s.sent = true
	return &v1.SyncFlagsResponse{FlagConfiguration: "{}"}, nil
}

func (c *scriptedClient) SyncFlags(context.Context, *v1.SyncFlagsRequest, ...grpc.CallOption) (
	syncv1grpc.FlagSyncService_SyncFlagsClient, error,
) {
	n := c.calls.Add(1)
	if n == 2 && c.recovers {
		return &oneShotStream{}, nil
	}
	if n == 1 {
		if c.firstErr != nil {
			return nil, c.firstErr
		}
		return &oneShotStream{}, nil
	}
	return nil, errors.New("the source is down")
}

type closedStreams struct{ closed atomic.Int32 }

func (*closedStreams) RecordStreamOpened(context.Context, string, string) {}
func (c *closedStreams) RecordStreamClosed(context.Context, string, string) {
	c.closed.Add(1)
}
func (*closedStreams) RecordReconnect(context.Context, string, string) {}

// TestReadinessCharacterization pins when the gRPC provider is ready and how it treats a failed first SyncFlags
// call stops it.
func TestReadinessCharacterization(t *testing.T) {
	t.Run("a failed first SyncFlags call is retried and the provider stays up", func(t *testing.T) {
		s := &Sync{
			Logger: logger.NewLogger(nil, false),
			URI:    "host:8013",
			client: &scriptedClient{firstErr: errors.New("refused"), recovers: true},
		}
		ctx, cancel := context.WithCancel(context.Background())
		deliveries := make(chan sync.DataSync, 2)
		stopped := make(chan error, 1)
		go func() { stopped <- s.Sync(ctx, deliveries) }()
		t.Cleanup(func() {
			cancel()
			select {
			case <-stopped:
			case <-time.After(10 * time.Second):
				t.Error("the sync provider did not stop")
			}
		})

		require.False(t, s.IsReady())
		select {
		case got := <-deliveries:
			require.Equal(t, sync.DataSync{FlagData: "{}", Source: "host:8013"}, got)
		case err := <-stopped:
			t.Fatalf("the provider stopped with %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("no delivery after the retry")
		}
		require.True(t, s.IsReady())
	})

	t.Run("ready on stream open, and still ready after the stream breaks", func(t *testing.T) {
		streams := &closedStreams{}
		s := &Sync{
			Logger:                logger.NewLogger(nil, false),
			URI:                   "host:8013",
			client:                &scriptedClient{},
			StreamMetricsRecorder: streams,
		}
		require.False(t, s.IsReady())

		ctx, cancel := context.WithCancel(context.Background())
		deliveries := make(chan sync.DataSync, 2)
		stopped := make(chan error, 1)
		go func() { stopped <- s.Sync(ctx, deliveries) }()
		t.Cleanup(func() {
			cancel()
			select {
			case <-stopped:
			case <-time.After(10 * time.Second):
				t.Error("the sync provider did not stop")
			}
		})

		select {
		case got := <-deliveries:
			require.Equal(t, sync.DataSync{FlagData: "{}", Source: "host:8013"}, got)
		case <-time.After(5 * time.Second):
			t.Fatal("no delivery")
		}
		require.True(t, s.IsReady())

		// the stream ended and every reconnect attempt fails: the provider keeps reporting ready
		require.Eventually(t, func() bool { return streams.closed.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
		time.Sleep(500 * time.Millisecond)
		require.True(t, s.IsReady())
	})
}
