package events

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPublishNoSubscriberAllocs(t *testing.T) {
	bus := NewBus()
	other := bus.Subscribe(SubscribeOptions{Kinds: []Kind{KindTracking}, Buffer: 1})
	defer other.Close()

	ev := Evaluation{FlagKey: "f", Value: "v", HasValue: true}
	change := ConfigurationChange{Source: "s", Added: []string{"a"}}

	require.Zero(t, testing.AllocsPerRun(100, func() { PublishEvaluation(bus, ev) }))
	require.Zero(t, testing.AllocsPerRun(100, func() { PublishConfigurationChange(bus, change) }))
	require.Zero(t, testing.AllocsPerRun(100, func() { PublishEvaluation(nil, ev) }))
	require.False(t, bus.HasSubscribers(KindEvaluation))
	require.True(t, bus.HasSubscribers(KindTracking))
}

func TestSubscriptionReceivesItsKindsOnly(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe(SubscribeOptions{Kinds: []Kind{KindEvaluation}, Buffer: 4})
	defer sub.Close()

	bus.Publish(ConfigurationChange{Source: "s"})
	bus.Publish(Evaluation{FlagKey: "f"})
	bus.Publish(Tracking{EventName: "e"})

	require.Equal(t, Evaluation{FlagKey: "f"}, <-sub.C())
	require.Empty(t, sub.C())
}

func TestFullBufferDropsAndCallsOnDrop(t *testing.T) {
	bus := NewBus()
	var dropped []Kind
	sub := bus.Subscribe(SubscribeOptions{
		Kinds: []Kind{KindEvaluation}, Buffer: 2, OnDrop: func(k Kind) { dropped = append(dropped, k) },
	})
	defer sub.Close()

	for i := 0; i < 5; i++ {
		bus.Publish(Evaluation{FlagKey: "f"})
	}

	require.Len(t, sub.C(), 2, "the channel is the only buffer")
	require.Equal(t, []Kind{KindEvaluation, KindEvaluation, KindEvaluation}, dropped, "once per dropped event")
}

func TestMissedMarksNextChange(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe(SubscribeOptions{Kinds: []Kind{KindConfigurationChange}, Buffer: 1})
	defer sub.Close()

	bus.Publish(ConfigurationChange{Version: "1"})
	bus.Publish(ConfigurationChange{Version: "2"}) // dropped
	bus.Publish(ConfigurationChange{Version: "3"}) // dropped

	first := (<-sub.C()).(ConfigurationChange)
	require.Equal(t, "1", first.Version)
	require.Zero(t, first.Missed)

	bus.Publish(ConfigurationChange{Version: "4"})
	next := (<-sub.C()).(ConfigurationChange)
	require.Equal(t, "4", next.Version)
	require.Equal(t, 2, next.Missed, "the two dropped changes")

	bus.Publish(ConfigurationChange{Version: "5"})
	require.Zero(t, (<-sub.C()).(ConfigurationChange).Missed, "the marker is not repeated")
}

func TestMissedCountsADroppedMarkerAgain(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe(SubscribeOptions{Kinds: []Kind{KindConfigurationChange}, Buffer: 1})
	defer sub.Close()

	bus.Publish(ConfigurationChange{Version: "1"})
	bus.Publish(ConfigurationChange{Version: "2"}) // dropped: 1 missed
	bus.Publish(ConfigurationChange{Version: "3"}) // dropped with the marker: 2 missed
	<-sub.C()
	bus.Publish(ConfigurationChange{Version: "4"})
	require.Equal(t, 2, (<-sub.C()).(ConfigurationChange).Missed)
}

func TestMissedIsPerSubscription(t *testing.T) {
	bus := NewBus()
	slow := bus.Subscribe(SubscribeOptions{Kinds: []Kind{KindConfigurationChange}, Buffer: 1})
	fast := bus.Subscribe(SubscribeOptions{Kinds: []Kind{KindConfigurationChange}, Buffer: 8})
	defer slow.Close()
	defer fast.Close()

	bus.Publish(ConfigurationChange{Version: "1"})
	bus.Publish(ConfigurationChange{Version: "2"})

	for i := 0; i < 2; i++ {
		require.Zero(t, (<-fast.C()).(ConfigurationChange).Missed)
	}
	<-slow.C()
	bus.Publish(ConfigurationChange{Version: "3"})
	require.Equal(t, 1, (<-slow.C()).(ConfigurationChange).Missed)
}

func TestPublishNeverBlocks(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe(SubscribeOptions{Kinds: []Kind{KindEvaluation, KindConfigurationChange}, Buffer: 1})
	defer sub.Close() // nobody reads

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 1000; j++ {
					bus.Publish(Evaluation{})
					bus.Publish(ConfigurationChange{})
				}
			}()
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("publishing blocked on a subscription nobody reads")
	}
}

func TestPublishAfterCloseDropped(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe(SubscribeOptions{Kinds: []Kind{KindEvaluation}, Buffer: 4})
	bus.Publish(Evaluation{FlagKey: "before"})

	bus.Close()
	bus.Publish(Evaluation{FlagKey: "after"})

	require.Equal(t, "before", (<-sub.C()).(Evaluation).FlagKey, "drained after close")
	_, open := <-sub.C()
	require.False(t, open, "closed once drained")
	require.False(t, bus.HasSubscribers(KindEvaluation))

	late := bus.Subscribe(SubscribeOptions{Kinds: []Kind{KindEvaluation}})
	_, open = <-late.C()
	require.False(t, open, "a subscription on a closed bus is closed")

	require.NotPanics(t, func() { sub.Close(); bus.Close() })
}

func TestCloseSubscriptionStopsDelivery(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe(SubscribeOptions{Kinds: []Kind{KindEvaluation}, Buffer: 2})
	require.True(t, bus.HasSubscribers(KindEvaluation))

	sub.Close()
	sub.Close()
	require.False(t, bus.HasSubscribers(KindEvaluation))
	bus.Publish(Evaluation{})
	_, open := <-sub.C()
	require.False(t, open)
}

func TestPublishOnNilBus(t *testing.T) {
	var bus *Bus
	require.NotPanics(t, func() {
		bus.Publish(Evaluation{})
		PublishEvaluation(bus, Evaluation{})
		require.False(t, bus.HasSubscribers(KindEvaluation))
	})
}

func TestPublishWhileClosing(t *testing.T) {
	bus := NewBus()
	sub := bus.Subscribe(SubscribeOptions{Kinds: []Kind{KindEvaluation}, Buffer: 16})
	go func() {
		for range sub.C() {
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				bus.Publish(Evaluation{})
			}
		}()
	}
	bus.Close()
	wg.Wait()
}
