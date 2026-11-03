package polling

import (
	"fmt"
	"hash/fnv"
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

// DefaultInterval is the polling interval, in seconds, of a source that configures none.
const DefaultInterval uint32 = 5

// ResolveInterval returns the polling interval of a source: DefaultInterval if it configures none. It fails for an
// interval beyond MaxInterval.
func ResolveInterval(interval uint32) (uint32, error) {
	if interval == 0 {
		interval = DefaultInterval
	}
	if interval > MaxInterval {
		return 0, fmt.Errorf("polling interval %ds exceeds maximum %ds", interval, MaxInterval)
	}
	return interval, nil
}
