package eventsink_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink/eventsinktest"
	"github.com/stretchr/testify/require"
)

// fakeSink records what it is given. fail decides the result of an attempt (counted from 1); block, while set,
// holds every delivery until it is released or the delivery's context ends.
type fakeSink struct {
	mu      sync.Mutex
	batches [][]events.Event
	calls   int
	fail    func(attempt int) error
	gate    chan struct{}
	closed  bool
}

func (s *fakeSink) Deliver(ctx context.Context, batch []events.Event) error {
	s.mu.Lock()
	s.calls++
	attempt, gate, fail := s.calls, s.gate, s.fail
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if fail != nil {
		if err := fail(attempt); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, append([]events.Event(nil), batch...))
	return nil
}

func (s *fakeSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *fakeSink) received() [][]events.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]events.Event(nil), s.batches...)
}

func (s *fakeSink) attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *fakeSink) stall() (release func()) {
	s.mu.Lock()
	s.gate = make(chan struct{})
	gate := s.gate
	s.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }
}

type fakeFactory struct {
	kinds []events.Kind
	sink  eventsink.Sink
	err   error
}

func (fakeFactory) Type() string { return "fake" }
func (f fakeFactory) Kinds() []events.Kind {
	if f.kinds == nil {
		return []events.Kind{events.KindEvaluation}
	}
	return f.kinds
}
func (f fakeFactory) Create(eventsink.Settings) (eventsink.Sink, error) { return f.sink, f.err }

func evaluation(i int) events.Event {
	return events.Evaluation{FlagKey: "flag", Variant: string(rune('a' + i%26))}
}

func count(batches [][]events.Event) int {
	n := 0
	for _, b := range batches {
		n += len(b)
	}
	return n
}

var fast = eventsink.WithBackoff(func(int) time.Duration { return time.Millisecond })

func start(t *testing.T, sink *fakeSink, config map[string]any, opts ...eventsink.Option) (*eventsink.Pipeline, *events.Bus, *eventsinktest.Drops) {
	t.Helper()
	bus, drops := events.NewBus(), eventsinktest.NewDrops()
	p, err := eventsink.NewPipeline(bus, fakeFactory{sink: sink}, eventsink.Settings{Telemetry: drops, Config: config}, append([]eventsink.Option{fast}, opts...)...)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = p.Close(ctx)
	})
	return p, bus, drops
}

func closeWithin(t *testing.T, p *eventsink.Pipeline, d time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return p.Close(ctx)
}

func TestPublisherNeverBlocked(t *testing.T) {
	sink := &fakeSink{}
	release := sink.stall()
	defer release()
	_, bus, drops := start(t, sink, map[string]any{"bufferSize": 2, "batchSize": 1, "maxAttempts": 1000})

	began := time.Now()
	for i := 0; i < 1000; i++ {
		bus.Publish(evaluation(i))
	}
	require.Less(t, time.Since(began), 500*time.Millisecond)
	require.GreaterOrEqual(t, drops.Count(eventsink.ReasonBufferFull), 990)
}

func TestBatchBySize(t *testing.T) {
	sink := &fakeSink{}
	p, bus, _ := start(t, sink, map[string]any{"batchSize": 3, "flushInterval": time.Hour})
	for i := 0; i < 7; i++ {
		bus.Publish(evaluation(i))
	}
	require.Eventually(t, func() bool { return count(sink.received()) == 6 }, 5*time.Second, time.Millisecond)
	require.NoError(t, closeWithin(t, p, 5*time.Second))

	sizes := []int{}
	for _, b := range sink.received() {
		sizes = append(sizes, len(b))
	}
	require.Equal(t, []int{3, 3, 1}, sizes)
}

func TestBatchByAge(t *testing.T) {
	sink := &fakeSink{}
	_, bus, _ := start(t, sink, map[string]any{"batchSize": 1000, "flushInterval": 30 * time.Millisecond})
	bus.Publish(evaluation(0))
	bus.Publish(evaluation(1))
	require.Eventually(t, func() bool { return count(sink.received()) == 2 }, 5*time.Second, time.Millisecond)
	require.Len(t, sink.received(), 1, "both events age out as one batch")
}

func TestRetryWithBackoff(t *testing.T) {
	sink := &fakeSink{fail: func(attempt int) error {
		if attempt < 3 {
			return errors.New("unavailable")
		}
		return nil
	}}
	var mu sync.Mutex
	var waited []int
	p, bus, drops := start(t, sink, map[string]any{"batchSize": 1}, eventsink.WithBackoff(func(attempt int) time.Duration {
		mu.Lock()
		defer mu.Unlock()
		waited = append(waited, attempt)
		return time.Millisecond
	}))
	bus.Publish(evaluation(0))
	require.NoError(t, closeWithin(t, p, 5*time.Second))

	require.Equal(t, 1, count(sink.received()))
	require.Equal(t, 3, sink.attempts())
	require.Equal(t, []int{1, 2}, waited)
	require.Zero(t, drops.Total())
}

func TestPermanentNotRetried(t *testing.T) {
	sink := &fakeSink{fail: func(int) error { return eventsink.Permanent(errors.New("bad request")) }}
	p, bus, drops := start(t, sink, map[string]any{"batchSize": 2, "flushInterval": time.Hour})
	bus.Publish(evaluation(0))
	bus.Publish(evaluation(1))
	require.NoError(t, closeWithin(t, p, 5*time.Second))

	require.Equal(t, 1, sink.attempts())
	require.Equal(t, 2, drops.Count(eventsink.ReasonPermanent))
	require.Zero(t, count(sink.received()))
}

func TestRetriesExhaustedDropped(t *testing.T) {
	sink := &fakeSink{fail: func(int) error { return errors.New("unavailable") }}
	p, bus, drops := start(t, sink, map[string]any{"batchSize": 1, "maxAttempts": 3})
	bus.Publish(evaluation(0))
	require.NoError(t, closeWithin(t, p, 5*time.Second))

	require.Equal(t, 3, sink.attempts())
	require.Equal(t, 1, drops.Count(eventsink.ReasonRetriesExhausted))
	require.Equal(t, 1, drops.Total())
}

// Every event the sink does not get is counted once: by the bus where the buffer was full, by the pipeline where
// it gave up.
func TestDropsCountedOnce(t *testing.T) {
	sink := &fakeSink{}
	release := sink.stall()
	defer release()
	p, bus, drops := start(t, sink, map[string]any{"bufferSize": 4, "batchSize": 1, "maxAttempts": 1000})
	const published = 30
	for i := 0; i < published; i++ {
		bus.Publish(evaluation(i))
	}
	require.Error(t, closeWithin(t, p, 50*time.Millisecond))

	require.Equal(t, published, drops.Total(), "nothing was delivered, so every event is counted exactly once")
	require.Positive(t, drops.Count(eventsink.ReasonBufferFull))
	require.Positive(t, drops.Count(eventsink.ReasonShutdown))
}

func TestMissedPassedThrough(t *testing.T) {
	sink := &fakeSink{}
	release := sink.stall()
	bus, drops := events.NewBus(), eventsinktest.NewDrops()
	p, err := eventsink.NewPipeline(bus, fakeFactory{kinds: []events.Kind{events.KindConfigurationChange}, sink: sink},
		eventsink.Settings{Telemetry: drops, Config: map[string]any{"bufferSize": 1, "batchSize": 1}}, fast)
	require.NoError(t, err)

	change := func(v string) events.Event { return events.ConfigurationChange{Source: "s", Version: v} }
	bus.Publish(change("1")) // taken by the pipeline, stalled in the sink
	require.Eventually(t, func() bool { return sink.attempts() == 1 }, 5*time.Second, time.Millisecond)
	bus.Publish(change("2")) // buffered
	bus.Publish(change("3")) // dropped
	require.Equal(t, 1, drops.Count(eventsink.ReasonBufferFull))
	release()
	require.Eventually(t, func() bool { return count(sink.received()) == 2 }, 5*time.Second, time.Millisecond)
	bus.Publish(change("4"))
	require.NoError(t, closeWithin(t, p, 5*time.Second))

	var last events.ConfigurationChange
	for _, b := range sink.received() {
		for _, e := range b {
			last = e.(events.ConfigurationChange)
		}
	}
	require.Equal(t, "4", last.Version)
	require.Equal(t, 1, last.Missed, "the drop travels with the next event")
}

func TestFlushOnClose(t *testing.T) {
	sink := &fakeSink{}
	p, bus, drops := start(t, sink, map[string]any{"batchSize": 1000, "flushInterval": time.Hour})
	for i := 0; i < 5; i++ {
		bus.Publish(evaluation(i))
	}
	require.NoError(t, closeWithin(t, p, 5*time.Second))
	require.Equal(t, 5, count(sink.received()))
	require.Zero(t, drops.Total())
	require.True(t, sink.closed, "the sink is closed after the last delivery")
	require.NoError(t, closeWithin(t, p, time.Second), "Close is safe to call twice")
}

func TestCloseWithinBudget(t *testing.T) {
	sink := &fakeSink{}
	release := sink.stall()
	defer release()
	p, bus, drops := start(t, sink, map[string]any{"batchSize": 1, "maxAttempts": 1000})
	for i := 0; i < 5; i++ {
		bus.Publish(evaluation(i))
	}
	require.Eventually(t, func() bool { return sink.attempts() == 1 }, 5*time.Second, time.Millisecond)

	began := time.Now()
	require.ErrorIs(t, closeWithin(t, p, 50*time.Millisecond), context.DeadlineExceeded)
	require.Less(t, time.Since(began), 400*time.Millisecond)
	require.Equal(t, 5, drops.Count(eventsink.ReasonShutdown))
}

// A sink that ignores its context cannot hold Close beyond its own.
func TestCloseDoesNotWaitForASinkIgnoringItsContext(t *testing.T) {
	stuck := make(chan struct{})
	defer close(stuck)
	sink := blocking{stuck}
	bus, drops := events.NewBus(), eventsinktest.NewDrops()
	p, err := eventsink.NewPipeline(bus, fakeFactory{sink: sink}, eventsink.Settings{Telemetry: drops, Config: map[string]any{"batchSize": 1}})
	require.NoError(t, err)
	bus.Publish(evaluation(0))
	bus.Publish(evaluation(1))
	time.Sleep(20 * time.Millisecond)

	began := time.Now()
	require.Error(t, closeWithin(t, p, 50*time.Millisecond))
	require.Less(t, time.Since(began), 400*time.Millisecond)
	require.Equal(t, 2, drops.Count(eventsink.ReasonShutdown))
}

type blocking struct{ stuck chan struct{} }

func (b blocking) Deliver(context.Context, []events.Event) error { <-b.stuck; return nil }

func TestBusCloseEndsPipeline(t *testing.T) {
	sink := &fakeSink{}
	p, bus, _ := start(t, sink, map[string]any{"batchSize": 1000, "flushInterval": time.Hour})
	bus.Publish(evaluation(0))
	bus.Close()

	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the pipeline did not end after the bus closed")
	}
	require.Equal(t, 1, count(sink.received()), "the buffered event is flushed first")
	require.True(t, sink.closed)
}

func TestNewPipelineErrors(t *testing.T) {
	bus := events.NewBus()
	create := func(f eventsink.Factory, config map[string]any) error {
		_, err := eventsink.NewPipeline(bus, f, eventsink.Settings{Config: config})
		return err
	}

	err := create(fakeFactory{sink: &fakeSink{}}, map[string]any{"batchSize": 0})
	require.ErrorContains(t, err, `"batchSize"`)
	require.ErrorContains(t, err, "fake")
	require.ErrorContains(t, create(fakeFactory{sink: &fakeSink{}}, map[string]any{"flushInterval": "soon"}), `"soon"`)
	require.ErrorContains(t, create(fakeFactory{err: errors.New("bad endpoint nope")}, nil), "bad endpoint nope")
	require.ErrorContains(t, create(fakeFactory{}, nil), "no sink")
	require.ErrorContains(t, create(fakeFactory{sink: &fakeSink{}, kinds: []events.Kind{"bogus"}}, nil), "bogus")
}

func TestParseOptions(t *testing.T) {
	o, err := eventsink.ParseOptions(nil)
	require.NoError(t, err)
	require.Equal(t, eventsink.Options{BufferSize: 10000, BatchSize: 100, FlushInterval: time.Second, MaxAttempts: 5}, o)

	o, err = eventsink.ParseOptions(map[string]any{
		"bufferSize": 7, "batchSize": int64(3), "maxAttempts": 2.0, "flushInterval": "250ms", "other": "ignored",
	})
	require.NoError(t, err)
	require.Equal(t, eventsink.Options{BufferSize: 7, BatchSize: 3, FlushInterval: 250 * time.Millisecond, MaxAttempts: 2}, o)

	for key, bad := range map[string]any{
		"bufferSize": -1, "batchSize": "ten", "maxAttempts": 1.5, "flushInterval": time.Duration(0),
	} {
		_, err := eventsink.ParseOptions(map[string]any{key: bad})
		require.ErrorContains(t, err, key)
	}
	_, err = eventsink.ParseOptions(map[string]any{"flushInterval": 5})
	require.ErrorContains(t, err, "flushInterval")
}

func TestPermanent(t *testing.T) {
	base := errors.New("refused")
	require.Nil(t, eventsink.Permanent(nil))
	require.False(t, eventsink.IsPermanent(base))
	wrapped := eventsink.Permanent(base)
	require.True(t, eventsink.IsPermanent(wrapped))
	require.True(t, eventsink.IsPermanent(errors.Join(errors.New("context"), wrapped)))
	require.ErrorIs(t, wrapped, base)
	require.Equal(t, "refused", wrapped.Error())
}

func TestValidateDeclaration(t *testing.T) {
	require.Error(t, eventsink.ValidateDeclaration(nil))
	require.NoError(t, eventsink.ValidateDeclaration(fakeFactory{}))
	require.Error(t, eventsink.ValidateDeclaration(fakeFactory{kinds: []events.Kind{}}))
}
