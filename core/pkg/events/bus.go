package events

import (
	"slices"
	"sync"
	"sync/atomic"
)

// SubscribeOptions configures a subscription.
type SubscribeOptions struct {
	// Kinds are the kinds of event the subscription receives.
	Kinds []Kind
	// Buffer is the capacity of the subscription's channel, its only buffer. A value below one means one.
	Buffer int
	// OnDrop is called once for each event dropped because the channel is full. It runs on the publisher's
	// goroutine and must neither block nor call into the bus.
	OnDrop func(Kind)
}

// Subscription delivers the events of its kinds.
type Subscription struct {
	bus    *Bus
	ch     chan Event
	kinds  []Kind
	onDrop func(Kind)
	// missed counts the configuration change events dropped since the last one delivered.
	missed atomic.Int64
}

// C is the channel the events arrive on. It is closed after Close, or after the bus closes, once drained.
func (s *Subscription) C() <-chan Event { return s.ch }

// Close ends the subscription. It is safe to call more than once.
func (s *Subscription) Close() { s.bus.unsubscribe(s) }

func (s *Subscription) wants(k Kind) bool { return slices.Contains(s.kinds, k) }

// Bus delivers events to subscriptions. Publishing never blocks: an event that finds a subscription's
// channel full is dropped for that subscription.
type Bus struct {
	// mu is held for reading while publishing and for writing while subscriptions change or the bus closes,
	// so a channel is never closed while a publisher sends to it.
	mu     sync.RWMutex
	subs   []*Subscription
	closed bool
	// counts holds the number of subscriptions per kind, for HasSubscribers.
	counts [3]atomic.Int32
}

// NewBus returns a bus without subscriptions.
func NewBus() *Bus { return &Bus{} }

func kindIndex(k Kind) int {
	switch k {
	case KindConfigurationChange:
		return 0
	case KindEvaluation:
		return 1
	case KindTracking:
		return 2
	}
	return -1
}

// Subscribe returns a subscription to the kinds in opts. On a closed bus the channel is closed already.
func (b *Bus) Subscribe(opts SubscribeOptions) *Subscription {
	kinds := slices.Compact(slices.Sorted(slices.Values(opts.Kinds)))
	s := &Subscription{bus: b, ch: make(chan Event, max(opts.Buffer, 1)), kinds: kinds, onDrop: opts.OnDrop}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		close(s.ch)
		return s
	}
	b.subs = append(b.subs, s)
	for _, k := range s.kinds {
		if i := kindIndex(k); i >= 0 {
			b.counts[i].Add(1)
		}
	}
	return s
}

func (b *Bus) unsubscribe(s *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	i := slices.Index(b.subs, s)
	if i < 0 {
		return
	}
	b.subs = slices.Delete(b.subs, i, i+1)
	for _, k := range s.kinds {
		if j := kindIndex(k); j >= 0 {
			b.counts[j].Add(-1)
		}
	}
	close(s.ch)
}

// HasSubscribers reports whether any subscription wants the kind. It is one atomic load.
func (b *Bus) HasSubscribers(k Kind) bool {
	i := kindIndex(k)
	return b != nil && i >= 0 && b.counts[i].Load() > 0
}

// Publish delivers e to every subscription of its kind, without blocking. A nil bus drops the event.
func (b *Bus) Publish(e Event) {
	if b == nil {
		return
	}
	kind := e.EventKind()
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}
	for _, s := range b.subs {
		if !s.wants(kind) {
			continue
		}
		out := e
		var missed int64
		if change, ok := e.(ConfigurationChange); ok {
			// the drops so far travel with this event; they are counted again if it is dropped too
			missed = s.missed.Swap(0)
			change.Missed = int(missed)
			out = change
		}
		select {
		case s.ch <- out:
		default:
			if kind == KindConfigurationChange {
				s.missed.Add(missed + 1)
			}
			if s.onDrop != nil {
				s.onDrop(kind)
			}
		}
	}
}

// Close stops delivery. Later publishes are dropped, and the subscriptions' channels close once drained.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for _, s := range b.subs {
		close(s.ch)
	}
	b.subs = nil
	for i := range b.counts {
		b.counts[i].Store(0)
	}
}

// PublishEvaluation publishes ev unless nothing subscribes to evaluation events; then it allocates nothing.
func PublishEvaluation(b *Bus, ev Evaluation) {
	if b != nil && b.HasSubscribers(KindEvaluation) {
		b.Publish(ev)
	}
}

// PublishTracking publishes ev unless nothing subscribes to tracking events; then it allocates nothing.
func PublishTracking(b *Bus, ev Tracking) {
	if b != nil && b.HasSubscribers(KindTracking) {
		b.Publish(ev)
	}
}

// PublishConfigurationChange publishes ev unless nothing subscribes to configuration change events; then it
// allocates nothing.
func PublishConfigurationChange(b *Bus, ev ConfigurationChange) {
	if b != nil && b.HasSubscribers(KindConfigurationChange) {
		b.Publish(ev)
	}
}
