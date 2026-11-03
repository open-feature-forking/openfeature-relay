package blobfetch

import (
	"context"
	"errors"
	"regexp"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/fetching"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
	"github.com/stretchr/testify/require"
)

const (
	sourceURI = "mock://bucket/dir/flags.json"
	flagsA    = `{"flags":{"a":{}}}`
	flagsB    = `{"flags":{"b":{}}}`
)

var scheme = regexp.MustCompile("^mock://.+?/")

// fakeStore is an object that the test changes while the fetcher reads it.
type fakeStore struct {
	mu       gosync.Mutex
	etag     string
	modified time.Time
	content  string
	failing  bool
	reads    atomic.Int64
}

func (s *fakeStore) set(etag, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.etag, s.content, s.modified = etag, content, time.Now()
}

func (s *fakeStore) fail() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failing = true
}

func (s *fakeStore) Identity(context.Context) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failing {
		return "", time.Time{}, errors.New("the bucket is down")
	}
	return s.etag, s.modified, nil
}

func (s *fakeStore) Read(context.Context) ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads.Add(1)
	return []byte(s.content), "application/json", nil
}

func factoryOf(store *fakeStore) Factory {
	return Factory{
		Kind: "mock", Prefix: "mock://", Scheme: scheme,
		Open: func(string, string) (Store, error) { return store, nil },
	}
}

func TestFetcherContract(t *testing.T) {
	store := &fakeStore{}
	store.set("etag-1", flagsA)

	synctest.RunFetcher(t, factoryOf(store), synctest.FetcherInputs{
		Source: sync.SourceConfig{URI: sourceURI, Provider: "mock"},
		Change: func(testing.TB) { store.set("etag-2", flagsB) },
		Break:  func(testing.TB) { store.fail() },
	})
}

func TestFetcher(t *testing.T) {
	store := &fakeStore{}
	store.set("etag-1", flagsA)
	f, err := factoryOf(store).CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{URI: "mock://bucket/dir/flags.json?region=x"}, Logger: logger.NewLogger(nil, false),
	})
	require.NoError(t, err)
	fetcher := f.(*Fetcher)
	require.Equal(t, "mock://bucket?region=x", fetcher.Bucket())
	require.Equal(t, "dir/flags.json", fetcher.Object())
	require.NoError(t, fetcher.Init(context.Background()))
	ctx := context.Background()

	first, err := fetcher.Fetch(ctx, "")
	require.NoError(t, err)
	require.Equal(t, flagsA, string(first.Data))
	require.Equal(t, ".json", first.Extension)
	require.Equal(t, "application/json", first.ContentType)
	require.Equal(t, int64(1), store.reads.Load())

	t.Run("the same ETag skips the read", func(t *testing.T) {
		res, err := fetcher.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.True(t, res.Unchanged)
		require.Equal(t, int64(1), store.reads.Load())
	})

	t.Run("a new ETag with the same body reads the object and carries the new version", func(t *testing.T) {
		store.set("etag-2", flagsA)
		res, err := fetcher.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.True(t, res.Unchanged)
		require.NotEqual(t, first.Version, res.Version)
		require.Equal(t, int64(2), store.reads.Load())
	})

	t.Run("a changed body is returned", func(t *testing.T) {
		store.set("etag-3", flagsB)
		res, err := fetcher.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.False(t, res.Unchanged)
		require.Equal(t, flagsB, string(res.Data))
	})

	t.Run("without an ETag the modification time identifies the object", func(t *testing.T) {
		store.mu.Lock()
		store.etag, store.modified, store.content = "", time.Unix(1_700_000_000, 0), flagsA
		store.mu.Unlock()
		res, err := fetcher.Fetch(ctx, "")
		require.NoError(t, err)

		same, err := fetcher.Fetch(ctx, res.Version)
		require.NoError(t, err)
		require.True(t, same.Unchanged)
		require.Empty(t, same.Version)
	})

	t.Run("a failed attributes call is reported with the object", func(t *testing.T) {
		store.fail()
		_, err := fetcher.Fetch(ctx, "")
		require.EqualError(t, err, "couldn't get object attributes: error fetching attributes for object "+
			"mock://bucket?region=x/dir/flags.json: the bucket is down")
	})

	t.Run("a source without a bucket or an object fails Init", func(t *testing.T) {
		noObject := New("mock", "mock://bucket/", "", nil, logger.NewLogger(nil, false))
		require.EqualError(t, noObject.Init(ctx), "no object string set")
		noBucket := New("mock", "", "flags.json", nil, logger.NewLogger(nil, false))
		require.EqualError(t, noBucket.Init(ctx), "no bucket string set")
	})
}

func TestFactory(t *testing.T) {
	f := factoryOf(&fakeStore{})
	require.Equal(t, "mock", f.Type())
	require.Equal(t, "blob", f.SourceType())
	require.Equal(t, []string{"mock://"}, f.Schemes())

	got, ok := f.ParseURI("mock://bucket/obj?x=1")
	require.True(t, ok)
	require.Equal(t, sync.SourceConfig{URI: "mock://bucket/obj?x=1", Provider: "mock"}, got)
	_, ok = f.ParseURI("s3://bucket/obj")
	require.False(t, ok)
}

func TestFactoryOpensTheStoreOfAnObject(t *testing.T) {
	var bucket, object string
	f := Factory{Kind: "mock", Prefix: "mock://", Scheme: scheme, Open: func(b, o string) (Store, error) {
		bucket, object = b, o
		return &fakeStore{}, nil
	}}
	_, err := f.CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{URI: "mock://bucket/dir/flags.json?region=x"}, Logger: logger.NewLogger(nil, false),
	})
	require.NoError(t, err)
	require.Equal(t, "mock://bucket?region=x", bucket)
	require.Equal(t, "dir/flags.json", object)

	t.Run("a refused source fails when the fetcher is created", func(t *testing.T) {
		refusing := Factory{Kind: "mock", Prefix: "mock://", Scheme: scheme, Open: func(string, string) (Store, error) {
			return nil, errors.New("invalid query parameter \"foo\"")
		}}
		_, err := refusing.CreateFetcher(sync.Settings{
			Source: sync.SourceConfig{URI: "mock://bucket/flags.json?foo=bar"}, Logger: logger.NewLogger(nil, false),
		})
		require.EqualError(t, err, `invalid query parameter "foo"`)
	})

	t.Run("a source without a bucket needs no store", func(t *testing.T) {
		f, err := Factory{Kind: "mock", Prefix: "mock://", Scheme: scheme}.CreateFetcher(sync.Settings{
			Source: sync.SourceConfig{URI: "malformed"}, Logger: logger.NewLogger(nil, false),
		})
		require.NoError(t, err)
		require.EqualError(t, f.(sync.InitializingFetcher).Init(context.Background()), "no bucket string set")
	})
}

// signalTrigger signals when the test asks.
type signalTrigger struct{ signals chan struct{} }

func (t signalTrigger) Run(ctx context.Context, signal func()) error {
	for {
		select {
		case <-t.signals:
			signal()
		case <-ctx.Done():
			return nil
		}
	}
}

type receivedRecorder struct {
	mu    gosync.Mutex
	calls [][3]string
}

func (r *receivedRecorder) RecordFlagConfigReceived(_ context.Context, sourceType, uri, selector string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, [3]string{sourceType, uri, selector})
}

// TestDeliveryRules pins what a blob source delivers, and when, as a fetching sync provider built around the fetcher.
func TestDeliveryRules(t *testing.T) {
	store := &fakeStore{}
	store.set("etag-1", flagsA)
	f, err := factoryOf(store).CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{URI: sourceURI}, Logger: logger.NewLogger(nil, false),
	})
	require.NoError(t, err)
	trigger := signalTrigger{make(chan struct{}, 1)}
	rec := &receivedRecorder{}
	s := fetching.New(fetching.Options{
		Fetcher: f, Triggers: []sync.Trigger{trigger}, Logger: logger.NewLogger(nil, false),
		Source: sourceURI, SourceType: sync.SourceBlob, Metrics: rec, IncrementalUpdates: true,
	})

	ctx, cancel := context.WithCancel(context.Background())
	deliveries := make(chan sync.DataSync, 8)
	stopped := make(chan error, 1)
	require.NoError(t, s.Init(ctx))
	go func() { stopped <- s.Sync(ctx, deliveries) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("the sync provider did not stop")
		}
	})

	requireDelivery := func(want string) {
		t.Helper()
		select {
		case got := <-deliveries:
			require.Equal(t, sync.DataSync{FlagData: want, Source: sourceURI, IncrementalUpdates: true}, got)
		case <-time.After(8 * time.Second):
			t.Fatalf("no delivery of %q", want)
		}
	}
	requireNoDelivery := func() {
		t.Helper()
		select {
		case got := <-deliveries:
			t.Fatalf("unexpected delivery of %q", got.FlagData)
		case <-time.After(300 * time.Millisecond):
		}
	}
	poll := func() { trigger.signals <- struct{}{} }

	requireDelivery(flagsA)
	require.Equal(t, int64(1), store.reads.Load())

	poll()
	requireNoDelivery()
	require.Equal(t, int64(1), store.reads.Load(), "the same ETag skips the read")

	store.set("etag-2", flagsA)
	poll()
	require.Eventually(t, func() bool { return store.reads.Load() == 2 }, 5*time.Second, 10*time.Millisecond)
	requireNoDelivery()

	store.set("etag-3", flagsB)
	poll()
	requireDelivery(flagsB)

	require.NoError(t, s.ReSync(ctx, deliveries))
	requireDelivery(flagsB)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Equal(t, [][3]string{{"blob", sourceURI, ""}, {"blob", sourceURI, ""}, {"blob", sourceURI, ""}}, rec.calls)
}

type closableStore struct {
	*fakeStore
	closed atomic.Int32
}

func (s *closableStore) Close() error { s.closed.Add(1); return nil }

func TestCloseClosesTheStore(t *testing.T) {
	store := &closableStore{fakeStore: &fakeStore{}}
	require.NoError(t, New("mock", "mock://bucket/", "flags.json", store, logger.NewLogger(nil, false)).Close())
	require.Equal(t, int32(1), store.closed.Load())

	require.NoError(t, New("mock", "mock://bucket/", "flags.json", &fakeStore{}, logger.NewLogger(nil, false)).Close(),
		"a store that cannot be closed is fine")
}
