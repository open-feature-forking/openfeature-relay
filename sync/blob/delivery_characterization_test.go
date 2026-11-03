package blob

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	stdsync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
)

const (
	sourceURI = "mock://bucket/flags.json"
	flagsA    = `{"flags":{"a":{}}}`
	flagsB    = `{"flags":{"b":{}}}`
)

// lockedDriver guards the fake driver's object, which the test changes while poll jobs read it.
type lockedDriver struct {
	mu stdsync.Mutex
	*fakeBlobDriver
}

func (d *lockedDriver) Attributes(ctx context.Context, key string) (*driver.Attributes, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fakeBlobDriver.Attributes(ctx, key)
}

func (d *lockedDriver) NewRangeReader(
	ctx context.Context, key string, offset, length int64, opts *driver.ReaderOptions,
) (driver.Reader, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fakeBlobDriver.NewRangeReader(ctx, key, offset, length, opts)
}

func (d *lockedDriver) setObject(etag, content string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.etag, d.content, d.modTime = etag, []byte(content), time.Now()
}

type lockedOpener struct{ drv *lockedDriver }

func (o lockedOpener) OpenBucketURL(context.Context, *url.URL) (*blob.Bucket, error) {
	return blob.NewBucket(o.drv), nil
}

func newBlobSync(t *testing.T, scheme string, drv *lockedDriver, recorder sync.MetricsRecorder) sync.ISync {
	t.Helper()

	mux := new(blob.URLMux)
	mux.RegisterBucket("mock", lockedOpener{drv})
	s, err := NewSync(
		scheme, regexp.MustCompile("^mock://.+?/"),
		// incrementalUpdates is set to show that deliveries do not carry it
		sync.SourceConfig{URI: sourceURI, Interval: 1, IncrementalUpdates: true},
		logger.NewLogger(nil, false), recorder, mux)
	require.NoError(t, err)
	return s
}

// TestDeliveryRulesCharacterization pins what a blob source delivers, and when, through NewSync and the
// ISync methods.
func TestDeliveryRulesCharacterization(t *testing.T) {
	t.Run("deliveries across polls", func(t *testing.T) {
		drv := &lockedDriver{fakeBlobDriver: &fakeBlobDriver{}}
		drv.setObject("etag-1", flagsA)
		rec := &receivedRecorder{}
		s := newBlobSync(t, "mock", drv, rec)

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
				require.Equal(t, sync.DataSync{FlagData: want, Source: sourceURI}, got)
			case <-time.After(8 * time.Second):
				t.Fatalf("no delivery of %q", want)
			}
		}
		requireNoDelivery := func() {
			t.Helper()
			select {
			case got := <-deliveries:
				t.Fatalf("unexpected delivery of %q", got.FlagData)
			case <-time.After(400 * time.Millisecond):
			}
		}

		// the initial fetch delivers
		requireDelivery(flagsA)
		require.Equal(t, int64(1), drv.reads.Load())

		// the same ETag skips the read
		time.Sleep(2500 * time.Millisecond)
		require.Equal(t, int64(1), drv.reads.Load())
		requireNoDelivery()

		// a new ETag with the same body reads the object and delivers nothing
		drv.setObject("etag-2", flagsA)
		require.Eventually(t, func() bool { return drv.reads.Load() == 2 }, 5*time.Second, 50*time.Millisecond)
		requireNoDelivery()

		// a changed body delivers
		drv.setObject("etag-3", flagsB)
		requireDelivery(flagsB)

		// a resync delivers although nothing changed
		require.NoError(t, s.ReSync(ctx, deliveries))
		requireDelivery(flagsB)

		// every delivery was recorded as received from a blob source named by the configured URI, without
		// a selector
		require.Equal(t, []receivedCall{
			{"blob", sourceURI, ""}, {"blob", sourceURI, ""}, {"blob", sourceURI, ""},
		}, rec.snapshot())
	})

	t.Run("a failed initial fetch keeps the provider up and not ready until the source answers", func(t *testing.T) {
		drv := &lockedDriver{fakeBlobDriver: &fakeBlobDriver{}}
		drv.setObject("etag-1", flagsA)
		gate := &gateOpener{opener: lockedOpener{drv}}
		mux := new(blob.URLMux)
		mux.RegisterBucket("mock", gate)
		s, err := NewSync(
			"mock", regexp.MustCompile("^mock://.+?/"), sync.SourceConfig{URI: sourceURI, Interval: 1},
			logger.NewLogger(nil, false), nil, mux)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		deliveries := make(chan sync.DataSync, 4)
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

		select {
		case err := <-stopped:
			t.Fatalf("the provider stopped with %v", err)
		case <-deliveries:
			t.Fatal("unexpected delivery")
		case <-time.After(1500 * time.Millisecond):
		}
		require.False(t, s.IsReady())

		gate.open.Store(true)
		select {
		case got := <-deliveries:
			require.Equal(t, sync.DataSync{FlagData: flagsA, Source: sourceURI}, got)
		case <-time.After(8 * time.Second):
			t.Fatal("no delivery once the source answered")
		}
		require.Eventually(t, s.IsReady, 5*time.Second, 10*time.Millisecond)
	})
}

type receivedCall struct{ sourceType, uri, selector string }

// receivedRecorder records the calls a provider makes to its sync.MetricsRecorder.
type receivedRecorder struct {
	mu    stdsync.Mutex
	calls []receivedCall
}

func (r *receivedRecorder) RecordFlagConfigReceived(_ context.Context, sourceType, uri, selector string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, receivedCall{sourceType, uri, selector})
}

func (r *receivedRecorder) snapshot() []receivedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]receivedCall(nil), r.calls...)
}

// brokenDriver fails every call once broken.
type brokenDriver struct {
	*lockedDriver
	failed atomic.Bool
}

func (d *brokenDriver) fail() { d.failed.Store(true) }

func (d *brokenDriver) Attributes(ctx context.Context, key string) (*driver.Attributes, error) {
	if d.failed.Load() {
		return nil, errors.New("the bucket is down")
	}
	return d.lockedDriver.Attributes(ctx, key)
}

type brokenOpener struct{ drv *brokenDriver }

func (o brokenOpener) OpenBucketURL(context.Context, *url.URL) (*blob.Bucket, error) {
	return blob.NewBucket(o.drv), nil
}

// gateOpener refuses to open its bucket until it is opened.
type gateOpener struct {
	opener lockedOpener
	open   atomic.Bool
}

func (g *gateOpener) OpenBucketURL(ctx context.Context, u *url.URL) (*blob.Bucket, error) {
	if !g.open.Load() {
		return nil, errors.New("the store does not answer yet")
	}
	return g.opener.OpenBucketURL(ctx, u)
}
