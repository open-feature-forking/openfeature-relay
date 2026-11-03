package polling

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"
)

// backoffCap is the longest wait of the backoff, unless the interval is longer.
const backoffCap = 60 * time.Second

// Backoff computes how long to wait after consecutive failures: nothing special after the first, then
// min(interval × 2^(failures−1), cap) plus a random jitter in [0, wait/2), where cap is the greater of the interval
// and 60 seconds.
type Backoff struct {
	Interval time.Duration
	// Rand returns a number in [0, 1); nil uses math/rand.
	Rand func() float64
}

// Delay returns the wait after the given number of consecutive failures, measured from the last failure. It is zero
// for no failure and for the first one, which stay on the normal schedule.
func (b Backoff) Delay(failures int) time.Duration {
	if failures < 2 {
		return 0
	}
	limit := max(b.Interval, backoffCap)
	wait := b.Interval
	for range failures - 1 {
		if wait >= limit {
			break
		}
		wait *= 2
	}
	wait = min(wait, limit)
	random := rand.Float64
	if b.Rand != nil {
		random = b.Rand
	}
	return wait + time.Duration(random()*float64(wait/2))
}

// Retry returns the wait before the next attempt of something that has no schedule of its own, such as the first
// fetch of a source: the interval after the first failure, Delay after the following ones.
func (b Backoff) Retry(failures int) time.Duration {
	if d := b.Delay(failures); d > 0 {
		return d
	}
	return b.Interval
}

// IntervalTrigger signals on the wall-clock-aligned schedule of OffsetSchedule. It backs off while the fetches it
// causes fail, using Backoff: the first retry stays on the schedule, later ones wait longer, and the first success
// returns to the schedule.
type IntervalTrigger struct {
	schedule OffsetSchedule
	backoff  Backoff

	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) bool

	mu        sync.Mutex
	failures  int
	noBackoff bool
	finished  chan struct{}
}

// NewIntervalTrigger creates an IntervalTrigger. As for NewCronPoller, an empty seed means offset 0, and it fails
// for an interval beyond MaxInterval.
func NewIntervalTrigger(interval uint32, intervalSeed string) (*IntervalTrigger, error) {
	poller, err := NewCronPoller(interval, intervalSeed)
	if err != nil {
		return nil, err
	}
	return &IntervalTrigger{
		schedule: OffsetSchedule{Interval: interval, Offset: poller.offset},
		backoff:  Backoff{Interval: time.Duration(interval) * time.Second},
		now:      time.Now,
		sleep:    sleep,
		finished: make(chan struct{}, 1),
	}, nil
}

// Offset returns the computed schedule offset in seconds.
func (t *IntervalTrigger) Offset() uint32 {
	return t.schedule.Offset
}

// Observe records the outcome of a fetch.
func (t *IntervalTrigger) Observe(err error) {
	t.mu.Lock()
	if err == nil {
		t.failures = 0
	} else {
		t.failures++
	}
	t.mu.Unlock()

	select {
	case t.finished <- struct{}{}:
	default:
	}
}

// Run signals on the schedule until ctx is done. After each signal it waits for the outcome of the fetch it caused
// before it computes the next wait.
func (t *IntervalTrigger) Run(ctx context.Context, signal func()) error {
	for {
		if !t.sleep(ctx, t.nextWait()) {
			return nil
		}
		select {
		case <-t.finished:
		default:
		}
		signal()
		select {
		case <-t.finished:
		case <-ctx.Done():
			return nil
		}
	}
}

func (t *IntervalTrigger) nextWait() time.Duration {
	t.mu.Lock()
	failures, noBackoff := t.failures, t.noBackoff
	t.mu.Unlock()

	if d := t.backoff.Delay(failures); d > 0 && !noBackoff {
		return d
	}
	now := t.now()
	return t.schedule.Next(now).Sub(now)
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// WithoutBackoff makes the trigger stay on its schedule whatever the fetches it causes did, as the poller did before
// it backed off. It returns the trigger.
func (t *IntervalTrigger) WithoutBackoff() *IntervalTrigger {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.noBackoff = true
	return t
}
