package source

import (
	"context"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/fetching"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
	"github.com/stretchr/testify/require"
)

const uri = Prefix + "allFlags"

func testSource() sync.SourceConfig { return sync.SourceConfig{URI: uri, Provider: Provider} }

func TestFetcherContract(t *testing.T) {
	s := New()
	require.NoError(t, s.Load([]byte(`{"flags":{}}`), "v1"))
	n := 1
	synctest.RunFetcher(t, s.Factory(), synctest.FetcherInputs{
		Source: testSource(),
		Change: func(testing.TB) {
			n++
			require.NoError(t, s.Load([]byte(`{"flags":{"a":{}}}`), "v"+string(rune('0'+n))))
		},
		Break: func(testing.TB) { s.Unload() },
	})
}

func TestTriggerContract(t *testing.T) {
	s := New()
	synctest.RunTrigger(t, s.Factory(), synctest.TriggerInputs{
		Source:    testSource(),
		Signal:    func(testing.TB) { _ = s.Load([]byte("{}"), "v") },
		Interrupt: func(testing.TB) {},
	})
}

// run starts a fetching provider over the source, the way a distribution does, and returns what it delivers.
func run(t *testing.T, s *Source, source sync.SourceConfig) <-chan sync.DataSync {
	t.Helper()
	f := s.Factory()
	fetcher, err := f.CreateFetcher(sync.Settings{Source: source})
	require.NoError(t, err)
	var triggers []sync.Trigger
	for _, c := range f.DefaultTriggers(source) {
		trigger, err := f.CreateTrigger(sync.Settings{Source: source, Config: c.Config})
		require.NoError(t, err)
		triggers = append(triggers, trigger)
	}
	provider := fetching.New(fetching.Options{
		Fetcher: fetcher, Triggers: triggers, Logger: logger.NewLogger(nil, false), Source: source.URI, SourceType: Provider,
		Interval: time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan sync.DataSync, 16)
	done := make(chan struct{})
	go func() { defer close(done); _ = provider.Sync(ctx, out) }()
	t.Cleanup(func() { cancel(); <-done })
	// the provider starts its triggers in goroutines: a load before a trigger watches the source signals no one, and
	// the hour-long poll interval leaves nothing else to fetch it
	require.Eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.signals) == len(triggers)
	}, 30*time.Second, time.Millisecond, "the triggers watch the source")
	return out
}

func receive(t *testing.T, out <-chan sync.DataSync) sync.DataSync {
	t.Helper()
	select {
	case d := <-out:
		return d
	case <-time.After(30 * time.Second):
		require.FailNow(t, "no delivery")
	}
	return sync.DataSync{}
}

func TestLoadFetchesNow(t *testing.T) {
	s := New()
	require.NoError(t, s.Load([]byte(`{"v":1}`), "d1"))
	out := run(t, s, testSource())
	require.JSONEq(t, `{"v":1}`, receive(t, out).FlagData)

	// the poll interval is an hour and no poller is attached: only the load can make this delivery
	require.NoError(t, s.Load([]byte(`{"v":2}`), "d2"))
	require.JSONEq(t, `{"v":2}`, receive(t, out).FlagData)
}

func TestFetchCarriesTheLoadedVersion(t *testing.T) {
	s := New()
	fetcher, err := s.Factory().CreateFetcher(sync.Settings{Source: testSource()})
	require.NoError(t, err)

	_, err = fetcher.Fetch(context.Background(), "")
	require.ErrorIs(t, err, ErrNothingLoaded)

	require.NoError(t, s.Load([]byte(`{"a":1}`), "sha256:abc"))
	res, err := fetcher.Fetch(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, "sha256:abc", res.Version, "the version is the one the owner gave, so the owner can tell its delivery")
	require.Equal(t, `{"a":1}`, string(res.Data))

	res, err = fetcher.Fetch(context.Background(), "sha256:abc")
	require.NoError(t, err)
	require.True(t, res.Unchanged)
	require.Equal(t, "sha256:abc", res.Version)
}

func TestLoadRefusesAnEmptyVersion(t *testing.T) {
	require.Error(t, New().Load([]byte("{}"), ""))
}

func TestLoadCopiesTheBytes(t *testing.T) {
	s := New()
	data := []byte(`{"a":1}`)
	require.NoError(t, s.Load(data, "v"))
	data[2] = 'x'
	fetcher, _ := s.Factory().CreateFetcher(sync.Settings{Source: testSource()})
	res, err := fetcher.Fetch(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, `{"a":1}`, string(res.Data))
	res.Data[2] = 'y'
	res, _ = fetcher.Fetch(context.Background(), "")
	require.Equal(t, `{"a":1}`, string(res.Data))
}

func TestUnchangedDocumentNotRedelivered(t *testing.T) {
	s := New()
	require.NoError(t, s.Load([]byte(`{"v":1}`), "d1"))
	out := run(t, s, testSource())
	receive(t, out)

	require.NoError(t, s.Load([]byte(`{"v":1}`), "d1"))
	select {
	case d := <-out:
		require.FailNow(t, "the same version was delivered again", d.FlagData)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestTriggerNotSelectable(t *testing.T) {
	f := New().Factory()
	_, err := f.CreateTrigger(sync.Settings{Source: sync.SourceConfig{URI: "file:flags.json", Provider: "file"}})
	require.ErrorContains(t, err, "cannot be selected")
	_, err = f.CreateFetcher(sync.Settings{Source: sync.SourceConfig{URI: "file:flags.json", Provider: "file"}})
	require.Error(t, err)
}

// the core attaches its poller to a fetcher unless the factory names its default triggers, so this is what keeps a
// testbed source from being polled
func TestNoPollerAttached(t *testing.T) {
	require.Equal(t, []sync.TriggerConfig{{Type: Provider}}, New().Factory().DefaultTriggers(testSource()))
}

func TestParseURI(t *testing.T) {
	f := New().Factory()
	got, ok := f.ParseURI(uri)
	require.True(t, ok)
	require.Equal(t, sync.SourceConfig{URI: uri, Provider: Provider}, got)
	_, ok = f.ParseURI("file:allFlags")
	require.False(t, ok)
}

func TestTriggerStopsWithContext(t *testing.T) {
	s := New()
	trigger, err := s.Factory().CreateTrigger(sync.Settings{Source: testSource()})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- trigger.Run(ctx, func() {}) }()
	cancel()
	require.NoError(t, <-done)
	require.Empty(t, s.signals, "a stopped trigger is no longer signalled")
}

// the owner loads before the runtime starts, so the first fetch does not fail and wait for its backoff
func TestLoadBeforeRunIsDeliveredAtOnce(t *testing.T) {
	s := New()
	require.NoError(t, s.Load([]byte(`{"v":1}`), "d1"))
	start := time.Now()
	out := run(t, s, testSource())
	require.JSONEq(t, `{"v":1}`, receive(t, out).FlagData)
	require.Less(t, time.Since(start), time.Second)
}

func TestFetcherRefusesOtherProviders(t *testing.T) {
	for _, provider := range []string{"", "file"} {
		_, err := New().Factory().CreateFetcher(sync.Settings{Source: sync.SourceConfig{URI: "file:f.json", Provider: provider}})
		require.Error(t, err, provider)
		_, err = New().Factory().CreateTrigger(sync.Settings{Source: sync.SourceConfig{URI: "file:f.json", Provider: provider}})
		require.Error(t, err, provider)
	}
}
