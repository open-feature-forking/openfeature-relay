package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

// idleSync is a sync provider that delivers nothing and stops with its context.
type idleSync struct{}

func (idleSync) Init(context.Context) error { return nil }
func (idleSync) Sync(ctx context.Context, _ chan<- coresync.DataSync) error {
	<-ctx.Done()
	return nil
}
func (idleSync) ReSync(context.Context, chan<- coresync.DataSync) error { return nil }
func (idleSync) IsReady() bool                                          { return true }

// nopFormat accepts nothing, for a runtime whose sources never deliver.
type nopFormat struct{}

func (nopFormat) Type() string { return "nop" }
func (nopFormat) Accept(context.Context, configformat.Input) (configformat.Result, error) {
	return configformat.Result{}, nil
}

// orderedEndpoint records when it returns.
type orderedEndpoint struct {
	name string
	log  func(string)
	stop time.Duration
}

func (e orderedEndpoint) Serve(ctx context.Context) error {
	<-ctx.Done()
	time.Sleep(e.stop)
	e.log("endpoint " + e.name + " returned")
	return nil
}

// TestShutdownOrder pins S6: every endpoint has returned before the event bus closes, and the shutdown hooks run
// after that, in the order they were registered, each with a context that carries the hook budget.
func TestShutdownOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	record := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, s)
	}

	bus := events.NewBus()
	sub := bus.Subscribe(events.SubscribeOptions{Kinds: []events.Kind{events.KindEvaluation}, Buffer: 1})
	go func() {
		for range sub.C() {
		}
		record("bus closed")
	}()

	rt := &Runtime{
		Logger:        logger.NewLogger(nil, false),
		Syncs:         []coresync.ISync{idleSync{}},
		DefaultFormat: nopFormat{},
		Bus:           bus,
		Endpoints: []NamedEndpoint{
			{Type: "slow", Endpoint: orderedEndpoint{name: "slow", log: record, stop: 200 * time.Millisecond}},
			{Type: "fast", Endpoint: orderedEndpoint{name: "fast", log: record}},
		},
	}
	for _, name := range []string{"first", "second"} {
		rt.onShutdown(name, func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			require.True(t, ok, "the hook budget is on the context")
			require.WithinDuration(t, time.Now().Add(hookBudget), deadline, 2*time.Second)
			// the bus is closed by now, so a late event is dropped
			bus.Publish(events.Evaluation{})
			time.Sleep(50 * time.Millisecond) // let the subscriber notice the closed channel
			record("hook " + name)
			return nil
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.start(ctx) }()
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("start did not return")
	}

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{
		"endpoint fast returned", "endpoint slow returned", "bus closed", "hook first", "hook second",
	}, order)
}
