package polling

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBackoffDelay(t *testing.T) {
	half := func() float64 { return 0.5 }
	tests := []struct {
		name     string
		interval time.Duration
		failures int
		want     time.Duration
	}{
		{"no failure", 5 * time.Second, 0, 0},
		{"the first failure stays on the schedule", 5 * time.Second, 1, 0},
		{"second failure", 5 * time.Second, 2, 10*time.Second + 5*time.Second/2},
		{"third failure", 5 * time.Second, 3, 20*time.Second + 10*time.Second/2},
		{"fourth failure", 5 * time.Second, 4, 40*time.Second + 20*time.Second/2},
		{"capped at 60 seconds", 5 * time.Second, 5, 60*time.Second + 30*time.Second/2},
		{"far beyond the cap", 5 * time.Second, 100, 60*time.Second + 30*time.Second/2},
		{"an interval longer than the cap is the cap", 120 * time.Second, 2, 120*time.Second + 60*time.Second/2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Backoff{Interval: tt.interval, Rand: half}.Delay(tt.failures))
		})
	}

	t.Run("jitter stays below half the wait", func(t *testing.T) {
		b := Backoff{Interval: 10 * time.Second, Rand: func() float64 { return 0.999999 }}
		require.Less(t, b.Delay(2), 30*time.Second)
		b.Rand = func() float64 { return 0 }
		require.Equal(t, 20*time.Second, b.Delay(2))
	})
}

func TestBackoffRetry(t *testing.T) {
	b := Backoff{Interval: 5 * time.Second, Rand: func() float64 { return 0 }}
	require.Equal(t, 5*time.Second, b.Retry(1))
	require.Equal(t, 10*time.Second, b.Retry(2))
}

// TestIntervalTriggerBackoff drives the trigger with a fake clock: the first retry stays on the schedule, the
// following ones back off with jitter, and the first success is aligned to the schedule again.
func TestIntervalTriggerBackoff(t *testing.T) {
	trigger, err := NewIntervalTrigger(30, "")
	require.NoError(t, err)

	sleeps := make(chan time.Duration)
	trigger.now = func() time.Time { return at(14, 3, 17) }
	trigger.sleep = func(ctx context.Context, d time.Duration) bool {
		select {
		case sleeps <- d:
			return true
		case <-ctx.Done():
			return false
		}
	}
	trigger.backoff.Rand = func() float64 { return 0.5 }

	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan struct{}, 8)
	done := make(chan error, 1)
	go func() { done <- trigger.Run(ctx, func() { signals <- struct{}{} }) }()

	// the schedule: 14:03:17 to 14:03:30
	onSchedule := 13 * time.Second
	steps := []struct {
		fetchErr error
		next     time.Duration
	}{
		{nil, onSchedule},
		{errors.New("down"), onSchedule},
		{errors.New("down"), 60*time.Second + 15*time.Second},
		{errors.New("down"), 60*time.Second + 15*time.Second},
		{nil, onSchedule},
	}
	require.Equal(t, onSchedule, <-sleeps)
	for i, step := range steps {
		<-signals
		trigger.Observe(step.fetchErr)
		require.Equal(t, step.next, <-sleeps, "after step %d", i)
	}

	cancel()
	require.NoError(t, <-done)
}

func TestIntervalTriggerStops(t *testing.T) {
	trigger, err := NewIntervalTrigger(1, "")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- trigger.Run(ctx, func() {}) }()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

func TestIntervalTriggerSignalsOnSchedule(t *testing.T) {
	trigger, err := NewIntervalTrigger(1, "")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan struct{}, 4)
	go func() { _ = trigger.Run(ctx, func() { signals <- struct{}{} }) }()

	select {
	case <-signals:
	case <-time.After(5 * time.Second):
		t.Fatal("no signal")
	}
}

func TestNewIntervalTrigger_ExceedsMaxInterval(t *testing.T) {
	_, err := NewIntervalTrigger(MaxInterval+1, "")
	require.Error(t, err)
}

func TestCronPollerRunsCallbackOnSchedule(t *testing.T) {
	poller, err := NewCronPoller(1, "")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	called := make(chan time.Time, 4)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		poller.Start(ctx, func() { called <- time.Now() })
	}()

	select {
	case at := <-called:
		require.Less(t, at.Nanosecond(), int(500*time.Millisecond), "fires at the start of a second, got %v", at)
	case <-time.After(5 * time.Second):
		t.Fatal("the callback did not run")
	}

	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after the context was cancelled")
	}
}

func TestCronPollerStartWaitsForRunningCallbacks(t *testing.T) {
	poller, err := NewCronPoller(1, "")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		poller.Start(ctx, func() {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
		})
	}()

	<-started
	cancel()
	select {
	case <-stopped:
		t.Fatal("Start returned while a callback was running")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return once the callback had")
	}
}

func TestIntervalTriggerWithoutBackoffStaysOnTheSchedule(t *testing.T) {
	trigger, err := NewIntervalTrigger(30, "")
	require.NoError(t, err)
	trigger.WithoutBackoff()

	sleeps := make(chan time.Duration)
	trigger.now = func() time.Time { return at(14, 3, 17) }
	trigger.sleep = func(ctx context.Context, d time.Duration) bool {
		select {
		case sleeps <- d:
			return true
		case <-ctx.Done():
			return false
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan struct{}, 8)
	go func() { _ = trigger.Run(ctx, func() { signals <- struct{}{} }) }()

	require.Equal(t, 13*time.Second, <-sleeps)
	for range 4 {
		<-signals
		trigger.Observe(errors.New("down"))
		require.Equal(t, 13*time.Second, <-sleeps)
	}
}
