package runtime

import (
	"context"
	"errors"
	"fmt"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"

	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

const resetTimeout = 10 * time.Second

func flagsJSON(keys ...string) string {
	body := ""
	for i, k := range keys {
		if i > 0 {
			body += ","
		}
		body += fmt.Sprintf(`%q:{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}`, k)
	}
	return `{"flags":{` + body + `}}`
}

// fakeSource is a sync provider the test controls. It delivers its content when the runtime starts it, and again on
// every ReSync, which waits for hold when the test has set one.
type fakeSource struct {
	source string

	mu      gosync.Mutex
	content string
	version string
	hold    chan struct{}
	data    chan<- coresync.DataSync
	resyncs atomic.Int32
}

func newFakeSource(source, content, version string) *fakeSource {
	return &fakeSource{source: source, content: content, version: version}
}

func (s *fakeSource) delivery() coresync.DataSync {
	s.mu.Lock()
	defer s.mu.Unlock()
	return coresync.DataSync{Source: s.source, FlagData: s.content, Version: s.version}
}

// set changes what the source holds.
func (s *fakeSource) set(content, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.content, s.version = content, version
}

func (s *fakeSource) holdResync() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hold = make(chan struct{})
	return s.hold
}

// push delivers what the source holds now, as a change of the source would.
func (s *fakeSource) push(ctx context.Context) {
	s.mu.Lock()
	data := s.data
	s.mu.Unlock()
	select {
	case data <- s.delivery():
	case <-ctx.Done():
	}
}

func (s *fakeSource) Init(context.Context) error { return nil }

func (s *fakeSource) Sync(ctx context.Context, data chan<- coresync.DataSync) error {
	s.mu.Lock()
	s.data = data
	s.mu.Unlock()
	select {
	case data <- s.delivery():
	case <-ctx.Done():
		return nil
	}
	<-ctx.Done()
	return nil
}

func (s *fakeSource) ReSync(ctx context.Context, data chan<- coresync.DataSync) error {
	s.resyncs.Add(1)
	s.mu.Lock()
	hold := s.hold
	s.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case data <- s.delivery():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *fakeSource) IsReady() bool { return true }

type resettable struct {
	rt      *Runtime
	store   *store.Store
	bus     *events.Bus
	sources []*fakeSource
	done    chan error
	cancel  context.CancelFunc
}

// newResettable runs a runtime over fake sources, each registered with the store, and waits until every one has been
// accepted.
func newResettable(t *testing.T, sources ...*fakeSource) *resettable {
	t.Helper()
	return newResettableWith(t, func(st *store.Store, log *logger.Logger) configformat.ConfigurationFormat {
		return flagdformat.NewFormat(st, log)
	}, sources...)
}

// newResettableWith is newResettable for the configuration format that format makes.
func newResettableWith(
	t *testing.T, format func(*store.Store, *logger.Logger) configformat.ConfigurationFormat, sources ...*fakeSource,
) *resettable {
	t.Helper()
	log := logger.NewLogger(nil, false)
	names := make([]string, 0, len(sources))
	syncs := make([]coresync.ISync, 0, len(sources))
	readiness := map[string]bool{}
	for _, s := range sources {
		names = append(names, s.source)
		syncs = append(syncs, s)
		readiness[s.source] = false
	}
	st, err := store.NewStore(log, names)
	require.NoError(t, err)
	bus := events.NewBus()
	rt := &Runtime{
		Logger:          log,
		DefaultFormat:   format(st, log),
		Bus:             bus,
		Syncs:           syncs,
		Endpoints:       []NamedEndpoint{{Type: "blocking", Endpoint: blockingEndpoint{stop: make(chan struct{})}}},
		sourceReadiness: readiness,
		store:           st,
	}

	ctx, cancel := context.WithCancel(context.Background())
	r := &resettable{rt: rt, store: st, bus: bus, sources: sources, done: make(chan error, 1), cancel: cancel}
	go func() { r.done <- rt.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(resetTimeout):
			t.Error("Run did not return")
		}
	})
	select {
	case <-rt.Initialized():
	case <-time.After(resetTimeout):
		t.Fatal("the runtime did not become initialized")
	}
	return r
}

func (r *resettable) flagKeys(t *testing.T) []string {
	t.Helper()
	flags, _, err := r.store.GetAll(context.Background(), &store.Selector{})
	require.NoError(t, err)
	keys := make([]string, 0, len(flags))
	for _, f := range flags {
		keys = append(keys, f.Key)
	}
	return keys
}

func TestRunStopsOnContext(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	r := newResettable(t, newFakeSource("a", flagsJSON("f1"), "v1"))
	require.Equal(t, []string{"f1"}, r.flagKeys(t))
	require.True(t, r.rt.isReady())

	r.cancel()
	select {
	case err := <-r.done:
		require.NoError(t, err, "a context that ends is a normal stop")
	case <-time.After(resetTimeout):
		t.Fatal("Run did not stop when its context ended")
	}
	r.done <- nil // for the cleanup
}

func TestResetBeforeRunIsAnError(t *testing.T) {
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"a"})
	require.NoError(t, err)
	rt := &Runtime{Logger: log, store: st}
	require.ErrorContains(t, rt.Reset(context.Background()), "not running")

	handBuilt := &Runtime{Logger: log}
	require.Error(t, handBuilt.Reset(context.Background()), "a runtime without a store has nothing to reset")
}

func TestResetEmptiesAndReloads(t *testing.T) {
	a := newFakeSource("a", flagsJSON("fa1", "fa2"), "v1")
	b := newFakeSource("b", flagsJSON("fb1"), "v1")
	r := newResettable(t, a, b)
	var mu gosync.Mutex
	var changes []events.ConfigurationChange
	sub := r.bus.Subscribe(events.SubscribeOptions{Kinds: []events.Kind{events.KindConfigurationChange}, Buffer: 16})
	t.Cleanup(sub.Close)
	go func() {
		for ev := range sub.C() {
			mu.Lock()
			changes = append(changes, ev.(events.ConfigurationChange))
			mu.Unlock()
		}
	}()
	require.ElementsMatch(t, []string{"fa1", "fa2", "fb1"}, r.flagKeys(t))

	// the reload of b waits, so the store is observed empty and the runtime not ready
	release := b.holdResync()
	reset := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
	defer cancel()
	go func() { reset <- r.rt.Reset(ctx) }()

	require.Eventually(t, func() bool { return a.resyncs.Load() == 1 && b.resyncs.Load() == 1 }, resetTimeout, time.Millisecond,
		"every source is reloaded, unchanged ones included")
	require.Eventually(t, func() bool { return len(r.flagKeys(t)) == 2 }, resetTimeout, time.Millisecond,
		"a is back while b is held")
	require.ElementsMatch(t, []string{"fa1", "fa2"}, r.flagKeys(t), "b's flags are gone until b is reloaded")
	require.False(t, r.rt.isReady(), "not ready while a source has not been accepted again")
	select {
	case err := <-reset:
		t.Fatalf("Reset returned before b was accepted: %v", err)
	default:
	}

	close(release)
	require.NoError(t, <-reset)
	require.ElementsMatch(t, []string{"fa1", "fa2", "fb1"}, r.flagKeys(t))
	require.True(t, r.rt.isReady())

	// the reload publishes the documents again, as a first start does, though their content did not change
	added := func(source string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, c := range changes {
			if c.Source == source {
				n += len(c.Added)
			}
		}
		return n
	}
	require.Eventually(t, func() bool { return added("a") == 2 && added("b") == 1 }, resetTimeout, time.Millisecond,
		"the reload published the documents of both sources again")
}

func TestResetClearsReadiness(t *testing.T) {
	a := newFakeSource("a", flagsJSON("fa"), "v1")
	b := newFakeSource("b", flagsJSON("fb"), "v1")
	r := newResettable(t, a, b)
	require.True(t, r.rt.isReady())

	release := b.holdResync()
	reset := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
	defer cancel()
	go func() { reset <- r.rt.Reset(ctx) }()

	require.Eventually(t, func() bool { return b.resyncs.Load() == 1 }, resetTimeout, time.Millisecond)
	// a is accepted again; b is not: the one-shot readiness of the first start must not carry over
	require.Eventually(t, func() bool { return len(r.flagKeys(t)) == 1 }, resetTimeout, time.Millisecond)
	for range 20 {
		require.False(t, r.rt.isReady(), "ready with a source that has not been accepted again")
		time.Sleep(time.Millisecond)
	}

	close(release)
	require.NoError(t, <-reset)
	require.True(t, r.rt.isReady())
}

func TestResetTwice(t *testing.T) {
	a := newFakeSource("a", flagsJSON("fa"), "v1")
	r := newResettable(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
	defer cancel()

	require.NoError(t, r.rt.Reset(ctx))
	require.True(t, r.rt.isReady())
	require.NoError(t, r.rt.Reset(ctx))
	require.True(t, r.rt.isReady())
	require.Equal(t, []string{"fa"}, r.flagKeys(t))
	require.Equal(t, int32(2), a.resyncs.Load())
}

func TestConcurrentResetsAreSerialized(t *testing.T) {
	a := newFakeSource("a", flagsJSON("fa"), "v1")
	r := newResettable(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
	defer cancel()

	var wg gosync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, r.rt.Reset(ctx))
		}()
	}
	wg.Wait()
	require.True(t, r.rt.isReady())
	require.Equal(t, []string{"fa"}, r.flagKeys(t))
}

func TestResetNamesTheSourcesThatAreNotAccepted(t *testing.T) {
	a := newFakeSource("a", flagsJSON("fa"), "v1")
	b := newFakeSource("b", flagsJSON("fb"), "v1")
	r := newResettable(t, a, b)

	// b's reload delivers a document the format rejects
	b.set(`{"flags":`, "v2")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := r.rt.Reset(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, `"b"`, "names the source that was not accepted")
	require.NotContains(t, err.Error(), `"a"`)
	require.False(t, r.rt.isReady())
}

// A delivery that arrives while Reset runs lands after the wipe or not at all: it never leaves the store holding a
// source's flags that the reset did not reload.
func TestResetDoesNotPublishBeforeTheWipe(t *testing.T) {
	a := newFakeSource("a", flagsJSON("old"), "v1")
	r := newResettable(t, a)

	a.set(flagsJSON("new"), "v2")
	release := a.holdResync()
	ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
	defer cancel()
	reset := make(chan error, 1)
	go func() { reset <- r.rt.Reset(ctx) }()
	require.Eventually(t, func() bool { return a.resyncs.Load() == 1 }, resetTimeout, time.Millisecond)

	require.Empty(t, r.flagKeys(t), "the wipe is done before the reload is delivered")
	close(release)
	require.NoError(t, <-reset)
	require.Equal(t, []string{"new"}, r.flagKeys(t))
}

func TestWaitAcceptedAfterAccept(t *testing.T) {
	a := newFakeSource("a", flagsJSON("f1"), "v1")
	r := newResettable(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
	defer cancel()

	t.Run("a version that was accepted returns at once", func(t *testing.T) {
		require.NoError(t, r.rt.WaitAccepted(ctx, "a", "v1"))
	})

	t.Run("a version that arrives later is waited for, and the store holds it then", func(t *testing.T) {
		waited := make(chan error, 1)
		go func() { waited <- r.rt.WaitAccepted(ctx, "a", "v2") }()
		select {
		case err := <-waited:
			t.Fatalf("WaitAccepted returned before v2 was delivered: %v", err)
		case <-time.After(50 * time.Millisecond):
		}

		a.set(flagsJSON("f1", "f2"), "v2")
		a.push(ctx)
		require.NoError(t, <-waited)
		require.ElementsMatch(t, []string{"f1", "f2"}, r.flagKeys(t), "the store holds the version when the wait returns")
	})

	t.Run("an older accepted version is still known", func(t *testing.T) {
		a.set(flagsJSON("f3"), "v3")
		a.push(ctx)
		require.NoError(t, r.rt.WaitAccepted(ctx, "a", "v3"))
		require.NoError(t, r.rt.WaitAccepted(ctx, "a", "v2"), "v3 replaced v2, which was accepted")
	})

	t.Run("a version that never comes ends with the context", func(t *testing.T) {
		short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
		defer stop()
		require.ErrorIs(t, r.rt.WaitAccepted(short, "a", "v9"), context.DeadlineExceeded)
	})

	t.Run("a source that is not configured is waited for until the context ends", func(t *testing.T) {
		short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
		defer stop()
		require.ErrorIs(t, r.rt.WaitAccepted(short, "nope", "v1"), context.DeadlineExceeded)
	})

	t.Run("a version is needed", func(t *testing.T) {
		require.Error(t, r.rt.WaitAccepted(ctx, "a", ""))
	})
}

func TestWaitAcceptedReturnsTheRejection(t *testing.T) {
	a := newFakeSource("a", flagsJSON("f1"), "v1")
	r := newResettable(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
	defer cancel()

	a.set(`{"flags": 5}`, "bad")
	waited := make(chan error, 1)
	go func() { waited <- r.rt.WaitAccepted(ctx, "a", "bad") }()
	a.push(ctx)

	err := <-waited
	require.Error(t, err)
	require.False(t, errors.Is(err, context.DeadlineExceeded), "the rejection ends the wait, not the context: %v", err)
	require.Equal(t, []string{"f1"}, r.flagKeys(t), "the store keeps what it had")
}

func TestWaitAcceptedAfterReset(t *testing.T) {
	a := newFakeSource("a", flagsJSON("f1"), "v1")
	r := newResettable(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
	defer cancel()
	require.NoError(t, r.rt.WaitAccepted(ctx, "a", "v1"))

	// a reset forgets the versions: until the reload is delivered, v1 is not accepted
	release := a.holdResync()
	reset := make(chan error, 1)
	go func() { reset <- r.rt.Reset(ctx) }()
	require.Eventually(t, func() bool { return a.resyncs.Load() == 1 }, resetTimeout, time.Millisecond)

	short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	require.ErrorIs(t, r.rt.WaitAccepted(short, "a", "v1"), context.DeadlineExceeded, "v1 is not in the store after the wipe")

	waited := make(chan error, 1)
	go func() { waited <- r.rt.WaitAccepted(ctx, "a", "v1") }()
	close(release)
	require.NoError(t, <-reset)
	require.NoError(t, <-waited, "the reload delivers the version again")
}

// fixedVersionFormat accepts every delivery as the same version of its source's document, with nothing added.
type fixedVersionFormat struct{}

func (fixedVersionFormat) Type() string { return "fixed" }

func (fixedVersionFormat) Accept(_ context.Context, in configformat.Input) (configformat.Result, error) {
	return configformat.Result{Documents: []configformat.DocumentReport{{FlagSet: in.Source, Version: "same"}}}, nil
}

// A redelivery of the version a document has publishes nothing, so only the forgotten versions make the reload of
// a document that did not change an event, as the start's was.
func TestResetPublishesTheReloadOfAnUnchangedDocument(t *testing.T) {
	a := newFakeSource("a", "x", "v1")
	r := newResettableWith(t, func(*store.Store, *logger.Logger) configformat.ConfigurationFormat { return fixedVersionFormat{} }, a)
	var mu gosync.Mutex
	published := 0
	sub := r.bus.Subscribe(events.SubscribeOptions{Kinds: []events.Kind{events.KindConfigurationChange}, Buffer: 16})
	t.Cleanup(sub.Close)
	go func() {
		for range sub.C() {
			mu.Lock()
			published++
			mu.Unlock()
		}
	}()
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return published
	}

	ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
	defer cancel()
	a.push(ctx) // a redelivery of the same version: nothing is published
	require.NoError(t, r.rt.WaitAccepted(ctx, "a", "v1"))
	time.Sleep(50 * time.Millisecond)
	require.Zero(t, count(), "a redelivery of the version the document has is no event")

	require.NoError(t, r.rt.Reset(ctx))
	require.Eventually(t, func() bool { return count() == 1 }, resetTimeout, time.Millisecond,
		"the reload is announced after a reset")
}
