package builder

import (
	"context"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const (
	deliveryTimeout = 8 * time.Second
	// quietPeriod is how long a test waits to conclude that nothing is delivered
	quietPeriod = 400 * time.Millisecond
)

// runningSource is a sync provider built by SyncsFromConfig and started the way the runtime starts it:
// Init, then Sync in its own goroutine.
type runningSource struct {
	provider   sync.ISync
	deliveries chan sync.DataSync
	syncErr    chan error
	ctx        context.Context
	logs       *observer.ObservedLogs
}

// buildSource builds the provider of one source through the builder's public entry point.
func buildSource(t *testing.T, cfg sync.SourceConfig, opts ...func(*SyncBuilder)) (sync.ISync, *observer.ObservedLogs) {
	t.Helper()

	core, logs := observer.New(zap.DebugLevel)
	sb := NewSyncBuilder()
	for _, opt := range opts {
		opt(sb)
	}
	syncs, err := sb.SyncsFromConfig([]sync.SourceConfig{cfg}, logger.NewLogger(zap.New(core), false))
	require.NoError(t, err)
	require.Len(t, syncs, 1)
	return syncs[0], logs
}

// startSource builds, initializes and starts the provider of one source. The provider is stopped when the
// test ends.
func startSource(t *testing.T, cfg sync.SourceConfig, opts ...func(*SyncBuilder)) *runningSource {
	t.Helper()

	provider, logs := buildSource(t, cfg, opts...)
	ctx, cancel := context.WithCancel(context.Background())
	src := &runningSource{
		provider:   provider,
		deliveries: make(chan sync.DataSync, 32),
		syncErr:    make(chan error, 1),
		ctx:        ctx,
		logs:       logs,
	}
	require.NoError(t, provider.Init(ctx))

	done := make(chan struct{})
	go func() {
		defer close(done)
		src.syncErr <- provider.Sync(ctx, src.deliveries)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the sync provider did not stop")
		}
	})
	return src
}

// requireDelivery waits for the next delivery and checks it.
func (s *runningSource) requireDelivery(t *testing.T, want sync.DataSync) {
	t.Helper()

	select {
	case got := <-s.deliveries:
		// the source version of a fetched delivery is the fetching provider's own tests' business
		got.Version = want.Version
		require.Equal(t, want, got)
	case err := <-s.syncErr:
		t.Fatalf("the provider stopped with %v while waiting for a delivery", err)
	case <-time.After(deliveryTimeout):
		t.Fatalf("no delivery of %q within %s", want.FlagData, deliveryTimeout)
	}
}

// requireNoDelivery waits for the quiet period and checks that nothing was delivered in it.
func (s *runningSource) requireNoDelivery(t *testing.T) {
	t.Helper()

	select {
	case got := <-s.deliveries:
		t.Fatalf("unexpected delivery of %q from %s", got.FlagData, got.Source)
	case <-time.After(quietPeriod):
	}
}

// resolveSource resolves the provider of one source through the builder.
func resolveSource(t *testing.T, cfg sync.SourceConfig, opts ...func(*SyncBuilder)) Resolution {
	t.Helper()

	sb := NewSyncBuilder()
	for _, opt := range opts {
		opt(sb)
	}
	res, err := sb.Resolve(cfg, logger.NewLogger(nil, false))
	require.NoError(t, err)
	return res
}

func loggerForTests() *logger.Logger { return logger.NewLogger(nil, false) }

// requireNoDeliveryWithin waits for the given time and checks that nothing was delivered in it.
func (s *runningSource) requireNoDeliveryWithin(t *testing.T, d time.Duration) {
	t.Helper()

	select {
	case got := <-s.deliveries:
		t.Fatalf("unexpected delivery of %q from %s", got.FlagData, got.Source)
	case <-time.After(d):
	}
}
