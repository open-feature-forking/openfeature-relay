package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	evalmock "github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/mock"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	flagsync "github.com/open-feature/flagd/flagd/pkg/service/flag-sync"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type readySync struct {
	ready bool
}

func (s readySync) Init(context.Context) error {
	return nil
}

func (s readySync) Sync(context.Context, chan<- coresync.DataSync) error {
	return nil
}

func (s readySync) ReSync(context.Context, chan<- coresync.DataSync) error {
	return nil
}

func (s readySync) IsReady() bool {
	return s.ready
}

type recordingSyncService struct {
	emitted []string
}

var _ flagsync.ISyncService = (*recordingSyncService)(nil)

func (s *recordingSyncService) Start(context.Context) error {
	return nil
}

func (s *recordingSyncService) Emit(source string) {
	s.emitted = append(s.emitted, source)
}

type signalingSyncService struct {
	emitted chan<- struct{}
}

func (s signalingSyncService) Start(context.Context) error {
	return nil
}

func (s signalingSyncService) Emit(string) {
	s.emitted <- struct{}{}
}

type controlledSync struct {
	payloads <-chan coresync.DataSync
	started  chan<- struct{}
}

func (s controlledSync) Init(context.Context) error {
	return nil
}

func (s controlledSync) Sync(ctx context.Context, dataSync chan<- coresync.DataSync) error {
	select {
	case s.started <- struct{}{}:
	case <-ctx.Done():
		return nil
	}

	for {
		select {
		case payload := <-s.payloads:
			select {
			case dataSync <- payload:
			case <-ctx.Done():
				return nil
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func (s controlledSync) ReSync(context.Context, chan<- coresync.DataSync) error {
	return nil
}

func (s controlledSync) IsReady() bool {
	return true
}

type blockingEvaluationService struct {
	stop <-chan struct{}
}

var _ service.IFlagEvaluationService = blockingEvaluationService{}

func (s blockingEvaluationService) Serve(ctx context.Context, _ service.Configuration) error {
	select {
	case <-s.stop:
		return errors.New("test complete")
	case <-ctx.Done():
		return nil
	}
}

func (blockingEvaluationService) Notify(service.Notification) {}

func (blockingEvaluationService) Shutdown() {}

type blockingOfrepService struct{}

func (blockingOfrepService) Start(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func requireSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()

	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for runtime signal")
	}
}

func TestRuntimeReadinessRequiresSuccessfulStateForEverySource(t *testing.T) {
	const (
		firstSource  = "file:/first.flagd.json"
		secondSource = "file:/second.flagd.json"
	)

	firstPayload := coresync.DataSync{Source: firstSource}
	secondPayload := coresync.DataSync{Source: secondSource}
	evaluatorError := errors.New("invalid flag configuration")

	ctrl := gomock.NewController(t)
	evaluator := evalmock.NewMockIEvaluator(ctrl)
	evaluator.EXPECT().SetState(firstPayload).Return(evaluatorError)
	evaluator.EXPECT().SetState(firstPayload).Return(nil)
	evaluator.EXPECT().SetState(secondPayload).Return(nil)

	syncService := &recordingSyncService{}
	runtime := Runtime{
		Evaluator:   evaluator,
		Logger:      logger.NewLogger(nil, false),
		SyncService: syncService,
		Syncs: []coresync.ISync{
			readySync{ready: true},
			readySync{ready: true},
		},
		sourceReadiness: map[string]bool{
			firstSource:  false,
			secondSource: false,
		},
	}

	require.False(t, runtime.isReady())

	runtime.updateAndEmit(firstPayload)
	require.False(t, runtime.isReady())

	runtime.updateAndEmit(firstPayload)
	require.False(t, runtime.isReady())

	runtime.updateAndEmit(secondPayload)
	require.True(t, runtime.isReady())
	require.Equal(t, []string{firstSource, secondSource}, syncService.emitted)
}

func TestRuntimeStartWaitsForEverySource(t *testing.T) {
	const (
		firstSource  = "file:/first.flagd.json"
		secondSource = "file:/second.flagd.json"
	)

	firstPayload := coresync.DataSync{Source: firstSource}
	secondPayload := coresync.DataSync{Source: secondSource}
	started := make(chan struct{}, 2)
	firstProviderData := make(chan coresync.DataSync)
	secondProviderData := make(chan coresync.DataSync)
	emitted := make(chan struct{}, 2)
	stop := make(chan struct{})

	ctrl := gomock.NewController(t)
	evaluator := evalmock.NewMockIEvaluator(ctrl)
	evaluator.EXPECT().SetState(firstPayload).Return(nil)
	evaluator.EXPECT().SetState(secondPayload).Return(nil)

	runtime := Runtime{
		Evaluator:         evaluator,
		Logger:            logger.NewLogger(nil, false),
		SyncService:       signalingSyncService{emitted: emitted},
		OfrepService:      blockingOfrepService{},
		EvaluationService: blockingEvaluationService{stop: stop},
		Syncs: []coresync.ISync{
			controlledSync{payloads: firstProviderData, started: started},
			controlledSync{payloads: secondProviderData, started: started},
		},
		sourceReadiness: map[string]bool{
			firstSource:  false,
			secondSource: false,
		},
	}

	startErr := make(chan error, 1)
	go func() {
		startErr <- runtime.Start()
	}()

	requireSignal(t, started)
	requireSignal(t, started)

	// Emit runs after the readiness bookkeeping, so waiting on it makes isReady deterministic.
	firstProviderData <- firstPayload
	requireSignal(t, emitted)
	require.False(t, runtime.isReady())

	secondProviderData <- secondPayload
	requireSignal(t, emitted)
	require.True(t, runtime.isReady())

	close(stop)
	require.Error(t, <-startErr)
}

// contactSync is a sync provider that reports when it last reached its source.
type contactSync struct {
	readySync
	last time.Time
}

func (s *contactSync) LastContact() time.Time { return s.last }

func TestReadinessLostWhenStale(t *testing.T) {
	const source = "file:/flags.json"
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	now := at
	provider := &contactSync{readySync: readySync{ready: true}, last: at}
	runtime := Runtime{
		Logger:      logger.NewLogger(nil, false),
		Syncs:       []coresync.ISync{provider},
		staleLimits: []staleLimit{{source: source, provider: provider, after: time.Minute}},
		now:         func() time.Time { return now },
	}
	runtime.initialized.Store(true)

	require.True(t, runtime.isReady())
	now = at.Add(time.Minute)
	require.True(t, runtime.isReady(), "exactly staleAfter is not past it")
	now = at.Add(time.Minute + time.Second)
	require.False(t, runtime.isReady(), "flagd is not ready once the source is past staleAfter")

	t.Run("readiness is back after a contact", func(t *testing.T) {
		provider.last = now
		require.True(t, runtime.isReady())
	})

	t.Run("a source that was never reached is stale", func(t *testing.T) {
		provider.last = time.Time{}
		require.False(t, runtime.isReady())
	})
}

func TestReadinessWithoutStaleAfterIgnoresContact(t *testing.T) {
	provider := &contactSync{readySync: readySync{ready: true}, last: time.Unix(1, 0)}
	runtime := Runtime{Logger: logger.NewLogger(nil, false), Syncs: []coresync.ISync{provider}}
	runtime.initialized.Store(true)

	require.True(t, runtime.isReady(), "a source never goes stale without the setting")
}

func TestStaleLimitsNeedAProviderThatReportsContact(t *testing.T) {
	sources := []coresync.SourceConfig{{URI: "a", Provider: "file"}, {URI: "b", Provider: "kubernetes", StaleAfter: 30}}

	_, err := staleLimitsOf(sources, []coresync.ISync{&contactSync{}, readySync{}})
	require.EqualError(t, err, `source "b" sets staleAfter, which the kubernetes sync provider cannot honour: it does not report its last contact`)

	sources[1].Provider = "grpc"
	limits, err := staleLimitsOf(sources, []coresync.ISync{readySync{}, &contactSync{}})
	require.NoError(t, err)
	require.Len(t, limits, 1)
	require.Equal(t, "b", limits[0].source)
	require.Equal(t, 30*time.Second, limits[0].after)
}
