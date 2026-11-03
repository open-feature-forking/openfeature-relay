package fetching

import (
	"context"
	"errors"
	"fmt"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

const (
	source  = "test://flags"
	flagsV1 = `{"flags":{"v1":{}}}`
	flagsV2 = `{"flags":{"v2":{}}}`
	timeout = 5 * time.Second
	quiet   = 150 * time.Millisecond
)

type fetcherFunc func(ctx context.Context, previous string) (sync.FetchResult, error)

func (f fetcherFunc) Fetch(ctx context.Context, previous string) (sync.FetchResult, error) {
	return f(ctx, previous)
}

func result(version, data string) sync.FetchResult {
	return sync.FetchResult{Data: []byte(data), Extension: ".json", Version: version}
}

// constantFetcher always returns the same flag configuration with the same version.
func constantFetcher() fetcherFunc {
	return func(context.Context, string) (sync.FetchResult, error) { return result("v1", flagsV1), nil }
}

// versionedFetcher returns a new version on every call and counts the calls.
func versionedFetcher(calls *atomic.Int32) fetcherFunc {
	return func(context.Context, string) (sync.FetchResult, error) {
		n := calls.Add(1)
		return result(fmt.Sprint("v", n), fmt.Sprintf(`{"flags":{"n%d":{}}}`, n)), nil
	}
}

// manualTrigger hands its signal function to the test and runs until the context is done.
type manualTrigger struct {
	signals chan func()
	runs    atomic.Int32
}

func newManualTrigger() *manualTrigger { return &manualTrigger{signals: make(chan func(), 8)} }

func (m *manualTrigger) Run(ctx context.Context, signal func()) error {
	m.runs.Add(1)
	m.signals <- signal
	<-ctx.Done()
	return nil
}

func (m *manualTrigger) signal(t *testing.T) func() {
	t.Helper()
	select {
	case s := <-m.signals:
		return s
	case <-time.After(timeout):
		t.Fatal("the trigger was not run")
		return nil
	}
}

// funcTrigger is a trigger with a Run of the test's choosing.
type funcTrigger func(ctx context.Context, signal func()) error

func (f funcTrigger) Run(ctx context.Context, signal func()) error { return f(ctx, signal) }

type running struct {
	provider   *Provider
	deliveries chan sync.DataSync
	done       chan error
	cancel     context.CancelFunc
}

func start(t *testing.T, opts Options) *running {
	t.Helper()

	if opts.Logger == nil {
		opts.Logger = logger.NewLogger(nil, false)
	}
	if opts.Source == "" {
		opts.Source = source
	}
	if opts.SourceType == "" {
		opts.SourceType = "test"
	}
	if opts.Interval == 0 {
		opts.Interval = 5 * time.Millisecond
	}
	r := &running{
		provider:   New(opts),
		deliveries: make(chan sync.DataSync, 32),
		done:       make(chan error, 1),
	}
	var ctx context.Context
	ctx, r.cancel = context.WithCancel(context.Background())
	require.NoError(t, r.provider.Init(ctx))
	go func() { r.done <- r.provider.Sync(ctx, r.deliveries) }()
	t.Cleanup(func() {
		r.cancel()
		select {
		case <-r.done:
		case <-time.After(timeout):
			t.Error("Sync did not return")
		}
	})
	return r
}

func (r *running) next(t *testing.T) sync.DataSync {
	t.Helper()
	select {
	case d := <-r.deliveries:
		return d
	case err := <-r.done:
		t.Fatalf("Sync returned %v while a delivery was expected", err)
	case <-time.After(timeout):
		t.Fatal("no delivery")
	}
	return sync.DataSync{}
}

func (r *running) none(t *testing.T) {
	t.Helper()
	select {
	case d := <-r.deliveries:
		t.Fatalf("unexpected delivery %q", d.FlagData)
	case <-time.After(quiet):
	}
}

// result waits for Sync to return and gives its error. The error stays available for the cleanup.
func (r *running) result(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.done:
		r.done <- err
		return err
	case <-time.After(timeout):
		t.Fatal("Sync did not return")
		return nil
	}
}

func (r *running) stop(t *testing.T) error {
	t.Helper()
	r.cancel()
	return r.result(t)
}

type receivedRecorder struct {
	mu    gosync.Mutex
	calls []string
}

func (r *receivedRecorder) RecordFlagConfigReceived(_ context.Context, sourceType, uri, selector string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, sourceType+" "+uri+" "+selector)
}

func (r *receivedRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// TestSignalBeforeTheFirstFetchIsHandledAfterIt: the triggers run before the first fetch, so that no change falls
// between it and the moment they watch the source; what they signal meanwhile follows the first fetch, never
// overlaps it.
func TestSignalBeforeTheFirstFetchIsHandledAfterIt(t *testing.T) {
	var calls, active, maxActive atomic.Int32
	gate := make(chan struct{})
	fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
		n := calls.Add(1)
		a := active.Add(1)
		defer active.Add(-1)
		if a > maxActive.Load() {
			maxActive.Store(a)
		}
		if n == 1 {
			<-gate
		}
		return result(fmt.Sprint("v", n), fmt.Sprintf(`{"n":%d}`, n)), nil
	})
	signalled := make(chan struct{})
	trigger := funcTrigger(func(ctx context.Context, signal func()) error {
		signal()
		close(signalled)
		<-ctx.Done()
		return nil
	})
	r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}})

	<-signalled
	require.Eventually(t, func() bool { return calls.Load() == 1 }, timeout, time.Millisecond)
	close(gate)

	require.Equal(t, `{"n":1}`, r.next(t).FlagData)
	require.Equal(t, `{"n":2}`, r.next(t).FlagData)
	require.Equal(t, int32(1), maxActive.Load())
}

func TestFirstFetchRetriesWithoutPoller(t *testing.T) {
	var calls atomic.Int32
	fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
		if calls.Add(1) < 3 {
			return sync.FetchResult{}, errors.New("down")
		}
		return result("v1", flagsV1), nil
	})
	r := start(t, Options{Fetcher: fetcher})

	require.Equal(t, sync.DataSync{FlagData: flagsV1, Source: source}, r.next(t))
	require.Equal(t, int32(3), calls.Load())
	require.Eventually(t, r.provider.IsReady, timeout, time.Millisecond)
}

func TestNotReadyBeforeFirstSuccess(t *testing.T) {
	fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
		return sync.FetchResult{}, errors.New("down")
	})
	r := start(t, Options{Fetcher: fetcher})
	r.none(t)
	require.False(t, r.provider.IsReady())
}

func TestFetchDeadlineUnblocksNextFetch(t *testing.T) {
	var calls atomic.Int32
	fetcher := fetcherFunc(func(ctx context.Context, _ string) (sync.FetchResult, error) {
		n := calls.Add(1)
		if n == 2 {
			<-ctx.Done() // a fetcher that never returns on its own
			return sync.FetchResult{}, ctx.Err()
		}
		return result(fmt.Sprint("v", n), fmt.Sprintf(`{"n":%d}`, n)), nil
	})
	trigger := newManualTrigger()
	r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}, Timeout: 50 * time.Millisecond})
	r.next(t)

	signal := trigger.signal(t)
	signal() // fetch 2 hangs until its deadline
	require.Eventually(t, func() bool { return calls.Load() == 2 }, timeout, time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	signal()
	require.Equal(t, `{"n":3}`, r.next(t).FlagData)
}

func TestSingleFlightOneTrailing(t *testing.T) {
	var calls, active, maxActive atomic.Int32
	gate := make(chan struct{})
	fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
		n := calls.Add(1)
		a := active.Add(1)
		defer active.Add(-1)
		for {
			m := maxActive.Load()
			if a <= m || maxActive.CompareAndSwap(m, a) {
				break
			}
		}
		if n == 2 {
			<-gate
		}
		return result(fmt.Sprint("v", n), fmt.Sprintf(`{"n":%d}`, n)), nil
	})
	trigger := newManualTrigger()
	r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}})
	r.next(t)

	signal := trigger.signal(t)
	signal()
	require.Eventually(t, func() bool { return calls.Load() == 2 }, timeout, time.Millisecond)
	for range 5 {
		signal() // during the fetch
	}
	close(gate)

	require.Equal(t, `{"n":2}`, r.next(t).FlagData)
	require.Equal(t, `{"n":3}`, r.next(t).FlagData) // the one trailing fetch
	r.none(t)
	require.Equal(t, int32(3), calls.Load())
	require.Equal(t, int32(1), maxActive.Load())
}

func TestUnchangedDeliversNothing(t *testing.T) {
	t.Run("the same source version", func(t *testing.T) {
		trigger := newManualTrigger()
		r := start(t, Options{Fetcher: constantFetcher(), Triggers: []sync.Trigger{trigger}})
		r.next(t)

		trigger.signal(t)()
		r.none(t)
	})

	// A result that is unchanged would deliver if the check were missing: the first carries data the core can decode,
	// the second none, which would be delivered as an empty flag configuration.
	for name, unchanged := range map[string]sync.FetchResult{
		"the fetcher reports unchanged with data":    {Unchanged: true, Data: []byte(flagsV2), Extension: ".json"},
		"the fetcher reports unchanged without data": {Unchanged: true},
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			fetcher := fetcherFunc(func(_ context.Context, previous string) (sync.FetchResult, error) {
				if calls.Add(1) > 1 {
					return unchanged, nil
				}
				return result("v1", flagsV1), nil
			})
			trigger := newManualTrigger()
			r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}})
			r.next(t)

			trigger.signal(t)()
			require.Eventually(t, func() bool { return calls.Load() == 2 }, timeout, time.Millisecond)
			r.none(t)
		})
	}
}

func TestFailedFetchDeliversNothing(t *testing.T) {
	var mu gosync.Mutex
	var previous []string
	var calls atomic.Int32
	fetcher := fetcherFunc(func(_ context.Context, p string) (sync.FetchResult, error) {
		mu.Lock()
		previous = append(previous, p)
		mu.Unlock()
		if calls.Add(1) == 1 {
			return result("v1", flagsV1), nil
		}
		return sync.FetchResult{}, errors.New("down")
	})
	trigger := newManualTrigger()
	r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}})
	r.next(t)

	signal := trigger.signal(t)
	signal()
	require.Eventually(t, func() bool { return calls.Load() == 2 }, timeout, time.Millisecond)
	r.none(t)
	signal()
	require.Eventually(t, func() bool { return calls.Load() == 3 }, timeout, time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"", "v1", "v1"}, previous, "the failed fetches keep the last accepted version")
}

func TestStaysReadyAfterFailures(t *testing.T) {
	var calls atomic.Int32
	fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
		if calls.Add(1) == 1 {
			return result("v1", flagsV1), nil
		}
		return sync.FetchResult{}, errors.New("down")
	})
	trigger := newManualTrigger()
	r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}})
	r.next(t)
	require.Eventually(t, r.provider.IsReady, timeout, time.Millisecond)

	signal := trigger.signal(t)
	signal()
	require.Eventually(t, func() bool { return calls.Load() == 2 }, timeout, time.Millisecond)
	require.True(t, r.provider.IsReady())
}

func TestFailingTriggerRestartedOthersRun(t *testing.T) {
	var failures atomic.Int32
	failing := funcTrigger(func(ctx context.Context, _ func()) error {
		failures.Add(1)
		return errors.New("lost the change signal")
	})
	healthy := newManualTrigger()
	var calls atomic.Int32
	r := start(t, Options{Fetcher: versionedFetcher(&calls), Triggers: []sync.Trigger{failing, healthy}})
	r.next(t)

	require.Eventually(t, func() bool { return failures.Load() >= 2 }, timeout, time.Millisecond, "the failing trigger is run again")
	healthy.signal(t)()
	require.Equal(t, `{"flags":{"n2":{}}}`, r.next(t).FlagData)
	require.NoError(t, r.stop(t))
}

func TestSeveralTriggersAnySignalFetches(t *testing.T) {
	a, b := newManualTrigger(), newManualTrigger()
	var calls atomic.Int32
	r := start(t, Options{Fetcher: versionedFetcher(&calls), Triggers: []sync.Trigger{a, b}})
	r.next(t)

	a.signal(t)()
	require.Equal(t, `{"flags":{"n2":{}}}`, r.next(t).FlagData)
	b.signal(t)()
	require.Equal(t, `{"flags":{"n3":{}}}`, r.next(t).FlagData)
}

func TestReSyncAlwaysDelivers(t *testing.T) {
	var mu gosync.Mutex
	var previous []string
	fetcher := fetcherFunc(func(_ context.Context, p string) (sync.FetchResult, error) {
		mu.Lock()
		previous = append(previous, p)
		mu.Unlock()
		return result("v1", flagsV1), nil
	})
	r := start(t, Options{Fetcher: fetcher})
	r.next(t)

	require.NoError(t, r.provider.ReSync(context.Background(), r.deliveries))
	require.Equal(t, sync.DataSync{FlagData: flagsV1, Source: source}, r.next(t))
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"", ""}, previous, "a resync asks for the full flag configuration")
}

func TestIncrementalUpdatesFromSource(t *testing.T) {
	for _, incremental := range []bool{false, true} {
		t.Run(fmt.Sprint(incremental), func(t *testing.T) {
			r := start(t, Options{Fetcher: constantFetcher(), IncrementalUpdates: incremental})
			require.Equal(t, incremental, r.next(t).IncrementalUpdates)
		})
	}
}

func TestShutdownWithBlockedSend(t *testing.T) {
	// the receiving end is gone: nothing reads the unbuffered channel
	provider := New(Options{
		Fetcher: constantFetcher(), Logger: logger.NewLogger(nil, false), Source: source, SourceType: "test",
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- provider.Sync(ctx, make(chan sync.DataSync)) }()

	time.Sleep(quiet)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(timeout):
		t.Fatal("Sync did not return while a delivery was blocked")
	}
}

type closingFetcher struct {
	fetcherFunc
	closed atomic.Int32
	inited atomic.Int32
	initFn func() error
}

func (c *closingFetcher) Close() error {
	c.closed.Add(1)
	return nil
}

func (c *closingFetcher) Init(context.Context) error {
	c.inited.Add(1)
	if c.initFn != nil {
		return c.initFn()
	}
	return nil
}

func TestShutdownClosesFetcher(t *testing.T) {
	fetcher := &closingFetcher{fetcherFunc: constantFetcher()}
	r := start(t, Options{Fetcher: fetcher})
	r.next(t)

	require.NoError(t, r.stop(t))
	require.Equal(t, int32(1), fetcher.closed.Load())
}

func TestInitInitializesFetcher(t *testing.T) {
	fetcher := &closingFetcher{fetcherFunc: constantFetcher(), initFn: func() error { return errors.New("no such path") }}
	provider := New(Options{Fetcher: fetcher, Logger: logger.NewLogger(nil, false), Source: source})

	err := provider.Init(context.Background())
	require.ErrorContains(t, err, "no such path")
	require.Equal(t, int32(1), fetcher.inited.Load())
}

func TestDecodesAndRecords(t *testing.T) {
	t.Run("yaml becomes json", func(t *testing.T) {
		fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
			return sync.FetchResult{Data: []byte("flags: {}\n"), Extension: ".yaml", Version: "v1"}, nil
		})
		r := start(t, Options{Fetcher: fetcher})
		require.Equal(t, `{"flags":{}}`, r.next(t).FlagData)
	})

	t.Run("the content type decodes when there is no extension", func(t *testing.T) {
		fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
			return sync.FetchResult{Data: []byte("flags: {}\n"), ContentType: "application/yaml", Version: "v1"}, nil
		})
		r := start(t, Options{Fetcher: fetcher})
		require.Equal(t, `{"flags":{}}`, r.next(t).FlagData)
	})

	t.Run("empty data is an empty flag configuration", func(t *testing.T) {
		fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
			return sync.FetchResult{Version: "v0"}, nil
		})
		r := start(t, Options{Fetcher: fetcher})
		require.Equal(t, "{}", r.next(t).FlagData)
	})

	t.Run("data that cannot be decoded is a failed fetch", func(t *testing.T) {
		fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
			return sync.FetchResult{Data: []byte("flags: [\n"), Extension: ".yaml", Version: "v1"}, nil
		})
		r := start(t, Options{Fetcher: fetcher})
		r.none(t)
		require.False(t, r.provider.IsReady())
	})

	t.Run("one record per delivery, naming type, source and no selector", func(t *testing.T) {
		recorder := &receivedRecorder{}
		var calls atomic.Int32
		trigger := newManualTrigger()
		r := start(t, Options{
			Fetcher: versionedFetcher(&calls), Triggers: []sync.Trigger{trigger}, Metrics: recorder, SourceType: "blob",
		})
		r.next(t)
		trigger.signal(t)()
		r.next(t)
		r.none(t)

		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		require.Equal(t, []string{"blob " + source + " ", "blob " + source + " "}, recorder.calls)
	})
}

type observingTrigger struct {
	*manualTrigger
	outcomes chan error
}

func (o observingTrigger) Observe(err error) { o.outcomes <- err }

func TestOutcomeObserverIsNotified(t *testing.T) {
	var calls atomic.Int32
	fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
		if calls.Add(1) == 2 {
			return sync.FetchResult{}, errors.New("down")
		}
		return result("v1", flagsV1), nil
	})
	trigger := observingTrigger{newManualTrigger(), make(chan error, 8)}
	r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}})
	r.next(t)

	require.NoError(t, <-trigger.outcomes)
	trigger.signal(t)()
	require.EqualError(t, <-trigger.outcomes, "error fetching "+source+": down")
}

func TestReadErrorKeepsTheFlags(t *testing.T) {
	var calls atomic.Int32
	fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
		if calls.Add(1) == 1 {
			return result("v1", flagsV1), nil
		}
		return sync.FetchResult{}, errors.New("permission denied")
	})
	trigger := newManualTrigger()
	r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}})
	r.next(t)
	trigger.signal(t)()
	r.none(t)
}

func TestEmptyDataIsDeliveredAndCounted(t *testing.T) {
	recorder := &receivedRecorder{}
	var calls atomic.Int32
	fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
		if calls.Add(1) == 2 {
			return sync.FetchResult{Version: "empty"}, nil
		}
		return result(fmt.Sprint("v", calls.Load()), flagsV1), nil
	})
	trigger := newManualTrigger()
	r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}, Metrics: recorder})
	r.next(t)
	trigger.signal(t)()
	require.Equal(t, "{}", r.next(t).FlagData)
	require.Equal(t, 2, recorder.count())
}

func TestUndecodableDataKeepsTheFlags(t *testing.T) {
	var calls atomic.Int32
	fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
		if calls.Add(1) == 1 {
			return result("v1", flagsV1), nil
		}
		return sync.FetchResult{Data: []byte("flags: [\n"), Extension: ".yaml", Version: "v2"}, nil
	})
	trigger := newManualTrigger()
	r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}})
	r.next(t)
	trigger.signal(t)()
	r.none(t)
}

func TestUnchangedAdoptsTheVersionItCarries(t *testing.T) {
	var mu gosync.Mutex
	var previous []string
	fetcher := fetcherFunc(func(_ context.Context, p string) (sync.FetchResult, error) {
		mu.Lock()
		previous = append(previous, p)
		mu.Unlock()
		if p == "" {
			return result("v1", flagsV1), nil
		}
		return sync.FetchResult{Unchanged: true, Version: "v1-moved"}, nil
	})
	trigger := newManualTrigger()
	r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}})
	r.next(t)

	fetches := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(previous)
	}
	// a signal that arrives before the fetch of the one before it has started is coalesced with it, so each waits
	signal := trigger.signal(t)
	signal()
	require.Eventually(t, func() bool { return fetches() >= 2 }, timeout, time.Millisecond)
	signal()
	require.Eventually(t, func() bool { return fetches() >= 3 }, timeout, time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"", "v1", "v1-moved"}, previous[:3])
}

type initializingTrigger struct {
	*manualTrigger
	err    error
	inited atomic.Int32
}

func (i *initializingTrigger) Init(context.Context) error {
	i.inited.Add(1)
	return i.err
}

func TestInitInitializesTriggers(t *testing.T) {
	trigger := &initializingTrigger{manualTrigger: newManualTrigger(), err: errors.New("no such path")}
	provider := New(Options{
		Fetcher: constantFetcher(), Triggers: []sync.Trigger{trigger}, Logger: logger.NewLogger(nil, false), Source: source,
	})

	err := provider.Init(context.Background())
	require.ErrorContains(t, err, "no such path")
	require.Equal(t, int32(1), trigger.inited.Load())
}

// TestRestartedTriggerQueuesAFetch: what a trigger would have signalled while it was not running is lost, so the
// provider fetches once when it runs the trigger again.
func TestRestartedTriggerQueuesAFetch(t *testing.T) {
	var fetches, runs atomic.Int32
	fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
		if fetches.Add(1) == 1 {
			return result("v1", flagsV1), nil
		}
		return result("v2", flagsV2), nil
	})
	trigger := funcTrigger(func(ctx context.Context, _ func()) error {
		if runs.Add(1) == 1 {
			return errors.New("the watch closed")
		}
		<-ctx.Done()
		return nil
	})
	r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}, Interval: 5 * time.Millisecond})

	require.Equal(t, flagsV1, r.next(t).FlagData)
	require.Equal(t, flagsV2, r.next(t).FlagData, "the change made while the trigger was not running is fetched")
}

func TestLastContactOnUnchangedFetch(t *testing.T) {
	var fail atomic.Bool
	fetcher := fetcherFunc(func(_ context.Context, previous string) (sync.FetchResult, error) {
		if fail.Load() {
			return sync.FetchResult{}, errors.New("down")
		}
		if previous == "v1" {
			return sync.FetchResult{Unchanged: true}, nil
		}
		return result("v1", flagsV1), nil
	})
	trigger := newManualTrigger()
	r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}})
	require.True(t, r.provider.LastContact().IsZero(), "no contact before the first fetch")
	r.next(t)
	first := r.provider.LastContact()
	require.False(t, first.IsZero())

	signal := trigger.signal(t)
	time.Sleep(5 * time.Millisecond)
	signal()
	require.Eventually(t, func() bool { return r.provider.LastContact().After(first) }, timeout, time.Millisecond,
		"an unchanged fetch is a contact, though it delivers nothing")
	r.none(t)

	second := r.provider.LastContact()
	fail.Store(true)
	time.Sleep(5 * time.Millisecond)
	signal()
	time.Sleep(quiet)
	require.Equal(t, second, r.provider.LastContact(), "a failed fetch is no contact")
}
