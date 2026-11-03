package polling

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"
)

// MaxInterval is the largest supported polling interval in seconds (1 day).
// OffsetSchedule uses seconds-since-midnight math, so intervals beyond a day
// would wrap around and produce incorrect fire times.
const MaxInterval uint32 = 86400

// OffsetSchedule is a cron.Schedule that fires every `interval` seconds,
// aligned to wall-clock time but shifted by `offset` seconds.
// For example, with interval=30 and offset=7, it fires at :07 and :37 of
// every minute. With offset=0, it is equivalent to the cron expression
// "*/interval * * * *" (using the 6-field format where the first field is seconds).
//
// This allows multiple instances with different offsets (derived from a seed)
// to avoid polling at the same instant (thundering herd), while remaining
// deterministic across restarts.
type OffsetSchedule struct {
	Interval uint32
	Offset   uint32
}

// Next returns the next activation time after t.
func (s OffsetSchedule) Next(t time.Time) time.Time {
	if s.Interval == 0 {
		return t.Add(time.Second)
	}

	// seconds since midnight in the local timezone
	hour, min, sec := t.Clock()
	now := int64(hour*3600 + min*60 + sec)
	interval := int64(s.Interval)
	offset := int64(s.Offset)

	// seconds since the last fire
	sinceLastFire := (now - offset%interval + interval) % interval
	lastFire := now - sinceLastFire

	// the next fire time
	nextFire := lastFire + interval

	delta := nextFire - now
	// truncate to the start of the current second, then add delta seconds
	return t.Truncate(time.Second).Add(time.Duration(delta) * time.Second)
}

// pollOffset computes a deterministic offset from a seed string.
// Returns 0 if seed is empty.
// fnv32a is a fast, well-distributed non-cryptographic hash;
// we only need even distribution across [0, interval), not collision resistance.
func pollOffset(seed string, interval uint32) uint32 {
	if seed == "" || interval == 0 {
		return 0
	}
	h := fnv.New32a()
	h.Write([]byte(seed))
	return h.Sum32() % interval
}

// Poller schedules a recurring callback. Start blocks until ctx is cancelled.
type Poller interface {
	Start(ctx context.Context, callback func())
	// Offset returns the schedule offset in seconds (0 when no seed is configured).
	Offset() uint32
}

// CronPoller is a Poller that fires on a wall-clock-aligned schedule with a deterministic offset derived from a
// seed. Every run of the callback is a goroutine of its own, so a slow callback does not delay the next one.
type CronPoller struct {
	interval uint32
	offset   uint32
	now      func() time.Time
}

// NewCronPoller creates a CronPoller. If intervalSeed is empty, offset defaults to
// 0 (equivalent to the legacy wall-clock-aligned behavior).
// Returns an error if interval exceeds MaxInterval.
func NewCronPoller(interval uint32, intervalSeed string) (*CronPoller, error) {
	if interval > MaxInterval {
		return nil, fmt.Errorf("polling interval %ds exceeds maximum %ds", interval, MaxInterval)
	}
	return &CronPoller{
		interval: interval,
		offset:   pollOffset(intervalSeed, interval),
		now:      time.Now,
	}, nil
}

// Offset returns the computed schedule offset in seconds.
func (p *CronPoller) Offset() uint32 {
	return p.offset
}

// Start runs the callback on the schedule and blocks until ctx is cancelled and the running callbacks have returned.
func (p *CronPoller) Start(ctx context.Context, callback func()) {
	schedule := OffsetSchedule{
		Interval: p.interval,
		Offset:   p.offset,
	}
	var running sync.WaitGroup
	defer running.Wait()

	for {
		now := p.now()
		timer := time.NewTimer(schedule.Next(now).Sub(now))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		running.Add(1)
		go func() {
			defer running.Done()
			callback()
		}()
	}
}

// DefaultInterval is the polling interval, in seconds, of a source that configures none.
const DefaultInterval uint32 = 5

// NewSourcePoller returns the interval of a source, DefaultInterval if it configures none, with a
// poller for it. It fails for an interval beyond MaxInterval.
func NewSourcePoller(interval uint32, intervalSeed string) (uint32, *CronPoller, error) {
	if interval == 0 {
		interval = DefaultInterval
	}
	poller, err := NewCronPoller(interval, intervalSeed)
	if err != nil {
		return 0, nil, err
	}
	return interval, poller, nil
}
