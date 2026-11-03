package eventsink

import (
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

// Defaults of the pipeline (UC-020 BR-002).
const (
	DefaultBufferSize    = 10000
	DefaultBatchSize     = 100
	DefaultFlushInterval = time.Second
	DefaultMaxAttempts   = 5

	initialBackoff = time.Second
	maxBackoff     = 30 * time.Second
)

// The core-owned keys of a sink's settings.
const (
	KeyBufferSize    = "bufferSize"
	KeyBatchSize     = "batchSize"
	KeyFlushInterval = "flushInterval"
	KeyMaxAttempts   = "maxAttempts"
)

// Options are the core's settings for one sink's pipeline.
type Options struct {
	// BufferSize is the number of events the sink's subscription holds; later events are dropped and counted.
	BufferSize int
	// BatchSize is the number of events that flushes a batch.
	BatchSize int
	// FlushInterval is the age of the oldest event of a batch that flushes it.
	FlushInterval time.Duration
	// MaxAttempts is the number of delivery attempts of a batch, the first included.
	MaxAttempts int
}

// ParseOptions reads the core-owned keys from a sink's settings; a missing key means its default. An invalid
// value is an error that names the key and the value.
func ParseOptions(config map[string]any) (Options, error) {
	o := Options{
		BufferSize: DefaultBufferSize, BatchSize: DefaultBatchSize,
		FlushInterval: DefaultFlushInterval, MaxAttempts: DefaultMaxAttempts,
	}
	for key, dst := range map[string]*int{
		KeyBufferSize: &o.BufferSize, KeyBatchSize: &o.BatchSize, KeyMaxAttempts: &o.MaxAttempts,
	} {
		v, ok := config[key]
		if !ok {
			continue
		}
		n, ok := positiveInt(v)
		if !ok {
			return Options{}, fmt.Errorf("event sink setting %q: %v is not a positive integer", key, v)
		}
		*dst = n
	}
	if v, ok := config[KeyFlushInterval]; ok {
		switch d := v.(type) {
		case time.Duration:
			o.FlushInterval = d
		case string:
			parsed, err := time.ParseDuration(d)
			if err != nil {
				return Options{}, fmt.Errorf("event sink setting %q: %q is not a duration", KeyFlushInterval, d)
			}
			o.FlushInterval = parsed
		default:
			return Options{}, fmt.Errorf("event sink setting %q: %v is not a duration", KeyFlushInterval, v)
		}
		if o.FlushInterval <= 0 {
			return Options{}, fmt.Errorf("event sink setting %q: %v is not positive", KeyFlushInterval, v)
		}
	}
	return o, nil
}

func positiveInt(v any) (int, bool) {
	var n int
	switch x := v.(type) {
	case int:
		n = x
	case int64:
		n = int(x)
	case float64:
		if x != math.Trunc(x) {
			return 0, false
		}
		n = int(x)
	default:
		return 0, false
	}
	return n, n > 0
}

// Option changes how a pipeline runs, for tests and embedders.
type Option func(*Pipeline)

// WithBackoff replaces the wait before attempt n+1 (n starts at 1). The default doubles from one second to
// thirty seconds and adds up to a quarter as jitter.
func WithBackoff(f func(attempt int) time.Duration) Option {
	return func(p *Pipeline) { p.backoff = f }
}

func defaultBackoff(attempt int) time.Duration {
	d := initialBackoff << min(attempt-1, 5)
	d = min(d, maxBackoff)
	return d + time.Duration(rand.Int64N(int64(d)/4+1))
}
