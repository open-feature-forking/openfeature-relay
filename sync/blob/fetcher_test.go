package blob

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
	"github.com/stretchr/testify/require"
	"gocloud.dev/blob"
)

func mockFactory(drv *lockedDriver) Factory {
	mux := new(blob.URLMux)
	mux.RegisterBucket("mock", lockedOpener{drv})
	return Factory{Kind: "mock", Prefix: "mock://", Scheme: regexp.MustCompile("^mock://.+?/"), Mux: mux}
}

func TestFetcherContract(t *testing.T) {
	drv := &lockedDriver{fakeBlobDriver: &fakeBlobDriver{}}
	drv.setObject("etag-1", flagsA)
	// the driver fails the attributes call of a bucket that has been broken
	broken := &brokenDriver{lockedDriver: drv}
	mux := new(blob.URLMux)
	mux.RegisterBucket("mock", brokenOpener{broken})
	f := Factory{Kind: "mock", Prefix: "mock://", Scheme: regexp.MustCompile("^mock://.+?/"), Mux: mux}

	synctest.RunFetcher(t, f, synctest.FetcherInputs{
		Source: sync.SourceConfig{URI: sourceURI, Provider: "mock"},
		Change: func(testing.TB) { drv.setObject("etag-2", flagsB) },
		Break:  func(testing.TB) { broken.fail() },
	})
}

func TestFetcher(t *testing.T) {
	drv := &lockedDriver{fakeBlobDriver: &fakeBlobDriver{}}
	drv.setObject("etag-1", flagsA)
	f, err := mockFactory(drv).CreateFetcher(sync.Settings{
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
	require.Equal(t, int64(1), drv.reads.Load())

	t.Run("the same ETag skips the read", func(t *testing.T) {
		res, err := fetcher.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.True(t, res.Unchanged)
		require.Equal(t, int64(1), drv.reads.Load())
	})

	t.Run("a new ETag with the same body reads the object and carries the new version", func(t *testing.T) {
		drv.setObject("etag-2", flagsA)
		res, err := fetcher.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.True(t, res.Unchanged)
		require.NotEqual(t, first.Version, res.Version)
		require.Equal(t, int64(2), drv.reads.Load())
	})

	t.Run("a changed body is returned", func(t *testing.T) {
		drv.setObject("etag-3", flagsB)
		res, err := fetcher.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.False(t, res.Unchanged)
		require.Equal(t, flagsB, string(res.Data))
	})

	t.Run("without an ETag the modification time identifies the object", func(t *testing.T) {
		at := time.Unix(1_700_000_000, 0)
		drv.mu.Lock()
		drv.etag, drv.modTime, drv.content = "", at, []byte(flagsA)
		drv.mu.Unlock()
		res, err := fetcher.Fetch(ctx, "")
		require.NoError(t, err)

		same, err := fetcher.Fetch(ctx, res.Version)
		require.NoError(t, err)
		require.True(t, same.Unchanged)
		require.Empty(t, same.Version)
	})

	t.Run("a source without a bucket or an object fails Init", func(t *testing.T) {
		noObject := NewFetcher("mock", regexp.MustCompile("^mock://.+?/"), "mock://bucket/", logger.NewLogger(nil, false), nil)
		require.EqualError(t, noObject.Init(ctx), "no object string set")
		noBucket := NewFetcher("mock", regexp.MustCompile("^mock://.+?/"), "", logger.NewLogger(nil, false), nil)
		require.EqualError(t, noBucket.Init(ctx), "no bucket string set")
	})
}

func TestFactory(t *testing.T) {
	f := mockFactory(&lockedDriver{fakeBlobDriver: &fakeBlobDriver{}})
	require.Equal(t, "mock", f.Type())
	require.Equal(t, "blob", f.SourceType())
	require.Equal(t, []string{"mock://"}, f.Schemes())

	got, ok := f.ParseURI("mock://bucket/obj?x=1")
	require.True(t, ok)
	require.Equal(t, sync.SourceConfig{URI: "mock://bucket/obj?x=1", Provider: "mock"}, got)
	_, ok = f.ParseURI("s3://bucket/obj")
	require.False(t, ok)
}
