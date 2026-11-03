package builder

import (
	"context"
	"net/http"
	"net/http/httptest"
	gosync "sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

const (
	flagsA = `{"flags":{"a":{"state":"ENABLED","variants":{"on":true},"defaultVariant":"on"}}}`
	flagsB = `{"flags":{"b":{"state":"ENABLED","variants":{"on":true},"defaultVariant":"on"}}}`
)

// flagServer is an HTTP flag source whose answer the test controls.
type flagServer struct {
	*httptest.Server

	mu       gosync.Mutex
	etag     string
	body     string
	status   int
	requests chan http.Header
}

func newFlagServer(t *testing.T, etag, body string) *flagServer {
	t.Helper()

	fs := &flagServer{etag: etag, body: body, status: http.StatusOK, requests: make(chan http.Header, 64)}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		defer fs.mu.Unlock()
		fs.requests <- r.Header.Clone()

		if fs.status != http.StatusOK {
			w.WriteHeader(fs.status)
			return
		}
		if fs.etag != "" {
			if r.Header.Get("If-None-Match") == fs.etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", fs.etag)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fs.body))
	}))
	t.Cleanup(fs.Close)
	return fs
}

func (fs *flagServer) serve(etag, body string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.etag, fs.body = etag, body
}

// nextRequest waits for the next request and returns its headers.
func (fs *flagServer) nextRequest(t *testing.T) http.Header {
	t.Helper()

	select {
	case h := <-fs.requests:
		return h
	case <-time.After(deliveryTimeout):
		t.Fatal("no request to the flag source")
		return nil
	}
}

// TestHTTPDeliveryCharacterization pins what an HTTP source delivers, and when.
func TestHTTPDeliveryCharacterization(t *testing.T) {
	t.Run("deliveries across polls", func(t *testing.T) {
		fs := newFlagServer(t, `"e1"`, flagsA)
		uri := fs.URL + "/flags"
		// incrementalUpdates is set: every delivery is an incremental update
		src := startSource(t, sync.SourceConfig{URI: uri, Provider: "http", Interval: 1, IncrementalUpdates: true})
		wantA := sync.DataSync{FlagData: flagsA, Source: uri, IncrementalUpdates: true}
		wantB := sync.DataSync{FlagData: flagsB, Source: uri, IncrementalUpdates: true}

		// the initial fetch delivers
		require.Empty(t, fs.nextRequest(t).Get("If-None-Match"))
		src.requireDelivery(t, wantA)

		// the next poll sends the ETag and gets 304: nothing is delivered
		require.Equal(t, `"e1"`, fs.nextRequest(t).Get("If-None-Match"))
		src.requireNoDelivery(t)

		// a new ETag with the same body delivers nothing
		fs.serve(`"e2"`, flagsA)
		fs.nextRequest(t)
		src.requireNoDelivery(t)

		// a changed body delivers
		fs.serve(`"e3"`, flagsB)
		fs.nextRequest(t)
		src.requireDelivery(t, wantB)

		// an empty 200 body delivers nothing, and does not reset what a change is compared with
		fs.serve(`"e4"`, "")
		fs.nextRequest(t)
		src.requireNoDelivery(t)
		fs.serve(`"e5"`, flagsB)
		fs.nextRequest(t)
		src.requireNoDelivery(t)
	})

	// ReSync runs without Sync here: the provider does not guard its state against a poll running at the same time
	t.Run("a resync ignores the ETag and always delivers", func(t *testing.T) {
		fs := newFlagServer(t, `"e1"`, flagsA)
		uri := fs.URL + "/flags"
		provider, _ := buildSource(t, sync.SourceConfig{URI: uri, Provider: "http", Interval: 1, IncrementalUpdates: true})
		require.NoError(t, provider.Init(context.Background()))
		deliveries := make(chan sync.DataSync, 2)

		for range 2 {
			require.NoError(t, provider.ReSync(context.Background(), deliveries))
			require.Empty(t, fs.nextRequest(t).Get("If-None-Match"))
			require.Equal(t, sync.DataSync{FlagData: flagsA, Source: uri, IncrementalUpdates: true}, <-deliveries)
		}
	})

	t.Run("a failed initial fetch keeps the provider up and not ready until the source answers", func(t *testing.T) {
		fs := newFlagServer(t, "", flagsA)
		fs.mu.Lock()
		fs.status = http.StatusInternalServerError
		fs.mu.Unlock()
		uri := fs.URL + "/flags"

		src := startSource(t, sync.SourceConfig{URI: uri, Provider: "http", Interval: 1})
		fs.nextRequest(t)
		fs.nextRequest(t) // it fetches again by itself
		src.requireNoDelivery(t)
		require.False(t, src.provider.IsReady())
		select {
		case err := <-src.syncErr:
			t.Fatalf("the provider stopped with %v", err)
		default:
		}

		fs.mu.Lock()
		fs.status = http.StatusOK
		fs.mu.Unlock()
		src.requireDelivery(t, sync.DataSync{FlagData: flagsA, Source: uri})
		require.Eventually(t, src.provider.IsReady, deliveryTimeout, 10*time.Millisecond)
	})

	t.Run("a source that is down does not stop the provider", func(t *testing.T) {
		fs := newFlagServer(t, "", "")
		uri := fs.URL + "/flags"
		fs.Close()

		src := startSource(t, sync.SourceConfig{URI: uri, Provider: "http", Interval: 1})
		src.requireNoDelivery(t)
		require.False(t, src.provider.IsReady())
		select {
		case err := <-src.syncErr:
			t.Fatalf("the provider stopped with %v", err)
		default:
		}
	})
}
