package sync

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	syncv1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	flagdService "github.com/open-feature-forking/openfeature-relay/service/transport"
	"github.com/stretchr/testify/require"
)

// closeSignallingStore reports when the store has closed a watcher channel.
type closeSignallingStore struct {
	*store.Store
	closed chan struct{}
}

func (s closeSignallingStore) Watch(ctx context.Context, selector *store.Selector, watcher chan<- store.FlagQueryResult) {
	inner := make(chan store.FlagQueryResult, 1)
	s.Store.Watch(ctx, selector, inner)
	go func() {
		for payload := range inner {
			watcher <- payload
		}
		close(watcher)
		close(s.closed)
	}()
}

// endingStream records what it sends; its first Send ends the stream (through end) and returns only once the store
// has closed the watcher, so that the handler then finds both the ended context and the closed channel ready.
type endingStream struct {
	mu     sync.Mutex
	sent   []string
	end    func()
	closed <-chan struct{}
}

func (r *endingStream) Send(resp *syncv1.SyncFlagsResponse) error {
	r.mu.Lock()
	r.sent = append(r.sent, resp.GetFlagConfiguration())
	first := len(r.sent) == 1
	r.mu.Unlock()
	if first {
		r.end()
		<-r.closed
	}
	return nil
}

func (r *endingStream) configurations() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sent...)
}

// TestStreamEndSendsNoEmptyConfiguration pins that a SyncFlags stream whose context ends, by the server's deadline or
// by the client, sends nothing more: the store closes the watcher channel when the context ends, and a read of the
// closed channel is not an update. The handler's select picks either ready case, so the test repeats.
func TestStreamEndSendsNoEmptyConfiguration(t *testing.T) {
	flagStore, err := store.NewStore(logger.NewLogger(nil, false), []string{})
	require.NoError(t, err)
	flagStore.Update("source", []model.Flag{{Key: "f1", State: "ENABLED", DefaultVariant: "on",
		Variants: map[string]any{"on": true}}}, nil, false)
	flags, _, err := flagStore.GetAll(context.Background(), nil)
	require.NoError(t, err)
	want, err := generateResponse(flags)
	require.NoError(t, err)

	for _, tc := range []struct {
		name     string
		deadline time.Duration
	}{
		{"server deadline", 10 * time.Millisecond},
		{"client cancel", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 50 {
				watched := closeSignallingStore{Store: flagStore, closed: make(chan struct{})}
				handler := syncHandler{
					store:           watched,
					log:             logger.NewLogger(nil, false),
					deadline:        tc.deadline,
					metricsRecorder: flagdService.NoopMetrics(),
				}
				ctx, cancel := context.WithCancel(context.Background())
				end := cancel
				if tc.deadline != 0 {
					end = func() {} // the server's deadline ends the stream
				}
				stream := &endingStream{end: end, closed: watched.closed}
				done := make(chan error, 1)
				go func() { done <- handler.syncFlags(ctx, nil, "", "", stream) }()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Fatal("the stream did not end")
				}
				cancel()
				require.Equal(t, []string{string(want)}, stream.configurations())
			}
		})
	}
}

// failingStore closes the watcher at once, as the store does when it cannot read the flags.
type failingStore struct{ *store.Store }

func (failingStore) Watch(_ context.Context, _ *store.Selector, watcher chan<- store.FlagQueryResult) {
	close(watcher)
}

// TestStoreErrorEndsStreamWithDataLoss pins that a watcher the store closes while the stream's context is live ends the
// stream with DataLoss, and sends nothing.
func TestStoreErrorEndsStreamWithDataLoss(t *testing.T) {
	flagStore, err := store.NewStore(logger.NewLogger(nil, false), []string{})
	require.NoError(t, err)
	handler := syncHandler{
		store:           failingStore{flagStore},
		log:             logger.NewLogger(nil, false),
		metricsRecorder: flagdService.NoopMetrics(),
	}
	stream := &endingStream{end: func() {}}
	done := make(chan error, 1)
	go func() { done <- handler.syncFlags(context.Background(), nil, "", "", stream) }()
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream did not end")
	}
	var connectErr *connect.Error
	require.True(t, errors.As(err, &connectErr), "%v", err)
	require.Equal(t, connect.CodeDataLoss, connectErr.Code())
	require.Empty(t, stream.configurations())
}
