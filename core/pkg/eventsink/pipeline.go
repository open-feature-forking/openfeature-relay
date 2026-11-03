package eventsink

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// DroppedMetric counts the events a sink did not get, with the attributes sink, kind and reason.
const DroppedMetric = "feature_flag.flagd.event_sink.dropped"

// The reasons an event is dropped.
const (
	ReasonBufferFull       = "buffer_full"
	ReasonRetriesExhausted = "retries_exhausted"
	ReasonPermanent        = "permanent"
	ReasonShutdown         = "shutdown"
)

// Pipeline feeds one sink from the event bus: it subscribes for the sink's kinds with a bounded buffer, batches
// the events, delivers the batches one at a time and retries failed ones. The subscription's channel is the only
// queue. Every event the sink does not get is counted once through the telemetry provider.
type Pipeline struct {
	sinkType string
	sink     Sink
	opts     Options
	sub      *events.Subscription
	dropped  telemetry.Int64Counter
	backoff  func(attempt int) time.Duration

	// deliverCtx ends the delivery in progress; Close cancels it when its context expires.
	deliverCtx context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	closeOnce  sync.Once
	closeErr   error

	mu sync.Mutex
	// batch holds the events collected for the next delivery and inflight those of the delivery in progress.
	batch    []events.Event
	inflight []events.Event
	// accounted is set once the undelivered events have been counted as dropped on shutdown.
	accounted bool
}

// NewPipeline creates the sink with the factory and starts delivering the events of its kinds from the bus. The
// core-owned keys of settings.Config size the pipeline (see ParseOptions); an invalid one, or a sink the factory
// rejects, is an error that names the sink.
func NewPipeline(bus *events.Bus, f Factory, settings Settings, opts ...Option) (*Pipeline, error) {
	if err := ValidateDeclarations(f); err != nil {
		return nil, fmt.Errorf("event sink: invalid declaration: %w", err)
	}
	o, err := ParseOptions(settings.Config)
	if err != nil {
		return nil, fmt.Errorf("event sink %q: %w", f.Type(), err)
	}
	if settings.Telemetry == nil {
		settings.Telemetry = telemetry.Noop()
	}
	sink, err := f.Create(settings)
	if err != nil {
		return nil, fmt.Errorf("event sink %q: %w", f.Type(), err)
	}
	if sink == nil {
		return nil, fmt.Errorf("event sink %q: the factory returned no sink", f.Type())
	}

	p := &Pipeline{
		sinkType: f.Type(), sink: sink, opts: o, backoff: defaultBackoff, done: make(chan struct{}),
		dropped: settings.Telemetry.Meter("flagd.eventsink").Int64Counter(DroppedMetric,
			telemetry.WithDescription("Events an event sink did not get"), telemetry.WithUnit("{event}")),
	}
	for _, opt := range opts {
		opt(p)
	}
	p.deliverCtx, p.cancel = context.WithCancel(context.Background())
	p.sub = bus.Subscribe(events.SubscribeOptions{
		Kinds: f.Kinds(), Buffer: o.BufferSize,
		OnDrop: func(k events.Kind) { p.drop(k, ReasonBufferFull, 1) },
	})
	go p.run()
	return p, nil
}

func (p *Pipeline) drop(kind events.Kind, reason string, n int) {
	p.dropped.Add(context.Background(), int64(n),
		telemetry.String("sink", p.sinkType), telemetry.String("kind", string(kind)), telemetry.String("reason", reason))
}

func (p *Pipeline) dropAll(batch []events.Event, reason string) {
	counts := map[events.Kind]int{}
	for _, e := range batch {
		counts[e.EventKind()]++
	}
	for kind, n := range counts {
		p.drop(kind, reason, n)
	}
}

// Close stops the subscription and waits for the events still buffered to be delivered. When ctx ends first, the
// delivery in progress is cancelled, the events not delivered are counted as dropped with the reason shutdown, and
// Close returns ctx's error without waiting for the sink. A bus that closed already has the same effect as
// the subscription closing. Close is safe to call more than once.
func (p *Pipeline) Close(ctx context.Context) error {
	p.closeOnce.Do(func() {
		p.sub.Close()
		select {
		case <-p.done:
		case <-ctx.Done():
			p.cancel()
			p.abandon()
			p.closeErr = ctx.Err()
		}
	})
	select {
	case <-p.done:
		return p.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Done is closed when the pipeline has delivered or dropped everything and closed the sink.
func (p *Pipeline) Done() <-chan struct{} { return p.done }

// abandon counts everything the pipeline still holds as dropped on shutdown.
func (p *Pipeline) abandon() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.accounted {
		return
	}
	p.accounted = true
	p.dropAll(p.inflight, ReasonShutdown)
	p.dropAll(p.batch, ReasonShutdown)
	p.inflight, p.batch = nil, nil
	// the channel is closed, so this ends when the buffer is empty
	var rest []events.Event
	for e := range p.sub.C() {
		rest = append(rest, e)
	}
	p.dropAll(rest, ReasonShutdown)
}

func (p *Pipeline) run() {
	defer close(p.done)
	defer func() {
		if c, ok := p.sink.(io.Closer); ok {
			_ = c.Close()
		}
	}()

	ch := p.sub.C()
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	for {
		select {
		case e, ok := <-ch:
			if !ok {
				p.flush()
				return
			}
			p.mu.Lock()
			if p.accounted {
				p.mu.Unlock()
				p.drop(e.EventKind(), ReasonShutdown, 1)
				return
			}
			p.batch = append(p.batch, e)
			size, first := len(p.batch), len(p.batch) == 1
			p.mu.Unlock()
			if size >= p.opts.BatchSize {
				timer.Stop()
				p.flush()
			} else if first {
				timer.Reset(p.opts.FlushInterval)
			}
		case <-timer.C:
			p.flush()
		}
	}
}

// flush delivers the collected batch, retrying it with backoff.
func (p *Pipeline) flush() {
	p.mu.Lock()
	if p.accounted || len(p.batch) == 0 || p.deliverCtx.Err() != nil {
		p.mu.Unlock()
		return
	}
	batch := p.batch
	p.batch, p.inflight = nil, batch
	p.mu.Unlock()

	reason := p.deliver(batch)

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.accounted || p.deliverCtx.Err() != nil {
		// Close is counting what is left, the batch included
		return
	}
	p.inflight = nil
	if reason != "" {
		p.dropAll(batch, reason)
	}
}

// deliver returns "" where the batch was delivered or the pipeline is being abandoned, else the reason it is dropped.
func (p *Pipeline) deliver(batch []events.Event) string {
	for attempt := 1; ; attempt++ {
		err := p.sink.Deliver(p.deliverCtx, batch)
		switch {
		case err == nil:
			return ""
		case p.deliverCtx.Err() != nil:
			// Close cancelled the delivery; it counts what is left
			return ""
		case IsPermanent(err):
			return ReasonPermanent
		case attempt >= p.opts.MaxAttempts:
			return ReasonRetriesExhausted
		}
		wait := time.NewTimer(p.backoff(attempt))
		select {
		case <-wait.C:
		case <-p.deliverCtx.Done():
			wait.Stop()
			return ""
		}
	}
}
