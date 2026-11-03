package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/stretchr/testify/require"
)

// recordingSink keeps the batches it is given. While gate is set, every delivery waits for it or for its context.
type recordingSink struct {
	mu      sync.Mutex
	batches [][]events.Event
	entered chan struct{}
	gate    chan struct{}
	closed  bool
}

func (s *recordingSink) Deliver(ctx context.Context, batch []events.Event) error {
	if s.entered != nil {
		select {
		case s.entered <- struct{}{}:
		default:
		}
	}
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, append([]events.Event(nil), batch...))
	return nil
}

func (s *recordingSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *recordingSink) entries() []events.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []events.Event
	for _, b := range s.batches {
		all = append(all, b...)
	}
	return all
}

func (s *recordingSink) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// recordingSinkFactory creates the sink it holds, or fails with err, and keeps the settings it got.
type recordingSinkFactory struct {
	name     string
	kinds    []events.Kind
	sink     *recordingSink
	err      error
	settings *eventsink.Settings
}

func (f recordingSinkFactory) Type() string {
	if f.name == "" {
		return "recording"
	}
	return f.name
}

func (f recordingSinkFactory) Kinds() []events.Kind {
	if f.kinds == nil {
		return []events.Kind{events.KindEvaluation}
	}
	return f.kinds
}

func (f recordingSinkFactory) Create(s eventsink.Settings) (eventsink.Sink, error) {
	if f.settings != nil {
		*f.settings = s
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.sink, nil
}

// dropCountingTelemetry is a telemetry factory whose provider counts the events the event sinks did not get, per
// reason, and keeps how much of the hook budget its Shutdown found.
type dropCountingTelemetry struct{ provider *dropCountingProvider }

func (*dropCountingTelemetry) Type() string { return "drop-counting" }

func (f *dropCountingTelemetry) Create(context.Context, telemetry.Settings) (telemetry.Provider, error) {
	f.provider = &dropCountingProvider{Provider: telemetry.Noop(), drops: map[string]int64{}}
	return f.provider, nil
}

type dropCountingProvider struct {
	telemetry.Provider
	mu       sync.Mutex
	drops    map[string]int64 // by reason
	shutdown []shutdownCall
}

type shutdownCall struct {
	live bool
	left time.Duration
}

func (p *dropCountingProvider) Meter(scope string) telemetry.Meter {
	return dropCountingMeter{Meter: p.Provider.Meter(scope), p: p}
}

func (p *dropCountingProvider) Shutdown(ctx context.Context) error {
	call := shutdownCall{live: ctx.Err() == nil}
	if deadline, ok := ctx.Deadline(); ok {
		call.left = time.Until(deadline)
	}
	p.mu.Lock()
	p.shutdown = append(p.shutdown, call)
	p.mu.Unlock()
	return nil
}

func (p *dropCountingProvider) dropped(reason string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.drops[reason]
}

type dropCountingMeter struct {
	telemetry.Meter
	p *dropCountingProvider
}

func (m dropCountingMeter) Int64Counter(name string, opts ...telemetry.InstrumentOption) telemetry.Int64Counter {
	if name != eventsink.DroppedMetric {
		return m.Meter.Int64Counter(name, opts...)
	}
	return dropCounter{m.p}
}

type dropCounter struct{ p *dropCountingProvider }

func (c dropCounter) Add(_ context.Context, n int64, attrs ...telemetry.Attribute) {
	for _, a := range attrs {
		if a.Key == "reason" {
			c.p.mu.Lock()
			c.p.drops[a.Value.AsString()] += n
			c.p.mu.Unlock()
		}
	}
}

func fromConfigWithSinks(t *testing.T, components Components) (*Runtime, error) {
	t.Helper()
	return FromConfig(logger.NewLogger(nil, false), "test", Config{SyncProviders: oneFileSource(t)}, components)
}

// TestInvalidEventSinkConfigStops pins UC-020 BR-009: an event sink that is declared wrongly, or that the factory or
// the core's own keys reject, stops startup with an error that names the event sink and the faulty value, and
// leaves nothing running.
func TestInvalidEventSinkConfigStops(t *testing.T) {
	tests := []struct {
		name       string
		configured func(t *testing.T) []eventsink.Configured
		contains   []string
	}{
		{
			name: "the factory rejects the configuration",
			configured: func(*testing.T) []eventsink.Configured {
				return []eventsink.Configured{{
					Factory: recordingSinkFactory{name: "webhook", err: errors.New(`setting "url": "ftp://x" is not an http URL`)},
				}}
			},
			contains: []string{`event sink "webhook"`, `setting "url": "ftp://x" is not an http URL`},
		},
		{
			name: "a core key is invalid",
			configured: func(*testing.T) []eventsink.Configured {
				return []eventsink.Configured{{
					Factory: recordingSinkFactory{name: "webhook", sink: &recordingSink{}},
					Config:  map[string]any{eventsink.KeyBufferSize: -3},
				}}
			},
			contains: []string{`event sink "webhook"`, `"bufferSize"`, "-3"},
		},
		{
			name: "the factory declares no kind",
			configured: func(*testing.T) []eventsink.Configured {
				return []eventsink.Configured{
					{Factory: recordingSinkFactory{name: "ok", sink: &recordingSink{}}},
					{Factory: recordingSinkFactory{name: "mute", kinds: []events.Kind{}, sink: &recordingSink{}}},
				}
			},
			contains: []string{"position 1", "no event kind"},
		},
		{
			name:       "the factory is nil",
			configured: func(*testing.T) []eventsink.Configured { return []eventsink.Configured{{}} },
			contains:   []string{"position 0", "factory is nil"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := &dropCountingTelemetry{}

			rt, err := fromConfigWithSinks(t, Components{Telemetry: factory, EventSinks: tt.configured(t)})

			require.Nil(t, rt)
			require.Error(t, err)
			for _, want := range tt.contains {
				require.ErrorContains(t, err, want)
			}
			require.Len(t, factory.provider.shutdown, 1, "the telemetry provider is released")
		})
	}
}

// TestEventSinkStartedBeforeAFailureIsClosed: when a later event sink is invalid, the one started before it is
// closed again.
func TestEventSinkStartedBeforeAFailureIsClosed(t *testing.T) {
	first := &recordingSink{}

	rt, err := fromConfigWithSinks(t, Components{EventSinks: []eventsink.Configured{
		{Factory: recordingSinkFactory{name: "first", sink: first}},
		{Factory: recordingSinkFactory{name: "second", err: errors.New("no")}},
	}})

	require.Nil(t, rt)
	require.ErrorContains(t, err, `event sink "second"`)
	require.Eventually(t, first.isClosed, 5*time.Second, 10*time.Millisecond)
}

// TestEventSinkGetsItsSettings: the sink's factory gets the runtime's telemetry provider, a logger and its own
// configuration, and its pipeline sizes itself from the core's keys.
func TestEventSinkGetsItsSettings(t *testing.T) {
	var got eventsink.Settings
	telemetryFactory := &dropCountingTelemetry{}
	config := map[string]any{"url": "http://sink", eventsink.KeyBatchSize: 7}

	rt, err := fromConfigWithSinks(t, Components{
		Telemetry: telemetryFactory,
		EventSinks: []eventsink.Configured{{
			Factory: recordingSinkFactory{sink: &recordingSink{}, settings: &got}, Config: config,
		}},
	})

	require.NoError(t, err)
	require.Same(t, telemetryFactory.provider, got.Telemetry)
	require.NotNil(t, got.Logger)
	require.Equal(t, config, got.Config)
	require.True(t, rt.Bus.HasSubscribers(events.KindEvaluation))
	require.False(t, rt.Bus.HasSubscribers(events.KindConfigurationChange), "only the kinds the factory declares")
}

// TestNoEventSinkNoSubscriber pins that without an event sink, as in flagd-full, nothing subscribes to the event bus
// and the only shutdown hook is the telemetry provider's.
func TestNoEventSinkNoSubscriber(t *testing.T) {
	rt, err := fromConfigWithSinks(t, Components{Telemetry: &dropCountingTelemetry{}})

	require.NoError(t, err)
	for _, kind := range []events.Kind{events.KindEvaluation, events.KindConfigurationChange, events.KindTracking} {
		require.False(t, rt.Bus.HasSubscribers(kind), kind)
	}
	require.Len(t, rt.hooks, 1)
	require.Equal(t, "telemetry", rt.hooks[0].name)
}

// TestSinksDeliverBufferedEventsOnShutdown: what an event sink still holds when the event bus closes is delivered
// before the shutdown hooks end, and the sink is closed.
func TestSinksDeliverBufferedEventsOnShutdown(t *testing.T) {
	sink := &recordingSink{}
	rt, err := fromConfigWithSinks(t, Components{EventSinks: []eventsink.Configured{{
		Factory: recordingSinkFactory{sink: sink},
		// the age of the batch would not flush it within the test
		Config: map[string]any{eventsink.KeyFlushInterval: "1h"},
	}}})
	require.NoError(t, err)
	for range 3 {
		events.PublishEvaluation(rt.Bus, events.Evaluation{FlagKey: "a"})
	}

	rt.Bus.Close()
	rt.runShutdownHooks()

	require.Len(t, sink.entries(), 3)
	require.True(t, sink.isClosed())
}

// TestSinksFlushedOnShutdown pins finding N6 and S6: with a stalled sink the flush gives up one second before the
// hook budget ends, the events it did not deliver are counted as dropped with the reason shutdown, and the telemetry
// provider's Shutdown, which runs next, gets a live context with at least that second left.
func TestSinksFlushedOnShutdown(t *testing.T) {
	const published = 5
	sink := &recordingSink{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	defer close(sink.gate)
	telemetryFactory := &dropCountingTelemetry{}
	rt, err := fromConfigWithSinks(t, Components{
		Telemetry: telemetryFactory,
		EventSinks: []eventsink.Configured{{
			Factory: recordingSinkFactory{sink: sink},
			Config:  map[string]any{eventsink.KeyBatchSize: 2, eventsink.KeyFlushInterval: "10ms"},
		}},
	})
	require.NoError(t, err)
	for range published {
		events.PublishEvaluation(rt.Bus, events.Evaluation{FlagKey: "a"})
	}
	select {
	case <-sink.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the sink got no delivery")
	}

	start := time.Now()
	rt.Bus.Close()
	rt.runShutdownHooks()
	elapsed := time.Since(start)

	require.Equal(t, int64(published), telemetryFactory.provider.dropped(eventsink.ReasonShutdown))
	require.GreaterOrEqual(t, elapsed, hookBudget-telemetryReserve-100*time.Millisecond, "the flush waits for its share")
	require.Less(t, elapsed, hookBudget-telemetryReserve+time.Second, "the flush stops before the budget ends")
	require.Len(t, telemetryFactory.provider.shutdown, 1)
	call := telemetryFactory.provider.shutdown[0]
	require.True(t, call.live, "the telemetry provider's context is alive")
	require.GreaterOrEqual(t, call.left, telemetryReserve-200*time.Millisecond)
}
