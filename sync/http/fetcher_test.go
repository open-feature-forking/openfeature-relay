package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	stdsync "sync"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
	"github.com/stretchr/testify/require"
)

const (
	bodyA = `{"flags":{"a":{}}}`
	bodyB = `{"flags":{"b":{}}}`
)

// flagSource is an HTTP flag source the test controls.
type flagSource struct {
	*httptest.Server

	mu     stdsync.Mutex
	etag   string
	body   string
	status int
	seen   []http.Header
}

func newFlagSource(t *testing.T, etag, body string) *flagSource {
	t.Helper()

	s := &flagSource{etag: etag, body: body, status: http.StatusOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.seen = append(s.seen, r.Header.Clone())
		if s.status != http.StatusOK {
			w.WriteHeader(s.status)
			return
		}
		if s.etag != "" {
			if r.Header.Get("If-None-Match") == s.etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", s.etag)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.body))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *flagSource) set(etag, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.etag, s.body = etag, body
}

func (s *flagSource) fail() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = http.StatusInternalServerError
}

func (s *flagSource) lastRequest() http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[len(s.seen)-1]
}

func TestFetcherContract(t *testing.T) {
	source := newFlagSource(t, `"e1"`, bodyA)
	synctest.RunFetcher(t, Factory{}, synctest.FetcherInputs{
		Source: sync.SourceConfig{URI: source.URL + "/flags", Provider: Provider},
		Change: func(testing.TB) { source.set(`"e2"`, bodyB) },
		Break:  func(testing.TB) { source.fail() },
	})
}

func TestFetcherConditionalRequests(t *testing.T) {
	source := newFlagSource(t, `"e1"`, bodyA)
	f := NewFetcher(sync.SourceConfig{URI: source.URL + "/flags"}, logger.NewLogger(nil, false))
	ctx := context.Background()

	first, err := f.Fetch(ctx, "")
	require.NoError(t, err)
	require.Equal(t, bodyA, string(first.Data))
	require.Equal(t, "application/json", first.ContentType)
	require.Empty(t, source.lastRequest().Get("If-None-Match"))

	t.Run("the ETag is sent and a 304 is unchanged", func(t *testing.T) {
		res, err := f.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.True(t, res.Unchanged)
		require.Equal(t, `"e1"`, source.lastRequest().Get("If-None-Match"))
	})

	t.Run("a new ETag with the same body is unchanged and carries the new version", func(t *testing.T) {
		source.set(`"e2"`, bodyA)
		res, err := f.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.True(t, res.Unchanged)
		require.NotEmpty(t, res.Version)
		require.NotEqual(t, first.Version, res.Version)

		again, err := f.Fetch(ctx, res.Version)
		require.NoError(t, err)
		require.True(t, again.Unchanged)
		require.Equal(t, `"e2"`, source.lastRequest().Get("If-None-Match"))
	})

	t.Run("a changed body is returned with a new version", func(t *testing.T) {
		source.set(`"e3"`, bodyB)
		res, err := f.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.False(t, res.Unchanged)
		require.Equal(t, bodyB, string(res.Data))
		require.NotEqual(t, first.Version, res.Version)
	})

	t.Run("an empty body is unchanged", func(t *testing.T) {
		source.set(`"e4"`, "")
		res, err := f.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.True(t, res.Unchanged)
	})

	t.Run("a full fetch sends no ETag", func(t *testing.T) {
		source.set(`"e5"`, bodyB)
		res, err := f.Fetch(ctx, "")
		require.NoError(t, err)
		require.Equal(t, bodyB, string(res.Data))
		require.Empty(t, source.lastRequest().Get("If-None-Match"))
	})
}

func TestFactory(t *testing.T) {
	f := Factory{}
	require.Equal(t, "http", f.Type())
	require.Equal(t, "http", f.SourceType())
	require.Equal(t, []string{"http://", "https://"}, f.Schemes())

	got, ok := f.ParseURI("https://host/flags.yaml?x=1")
	require.True(t, ok)
	require.Equal(t, sync.SourceConfig{URI: "https://host/flags.yaml?x=1", Provider: "http"}, got)
	_, ok = f.ParseURI("file:/tmp/x")
	require.False(t, ok)
}

func TestFetcherSendsHeaders(t *testing.T) {
	source := newFlagSource(t, "", bodyA)
	f := NewFetcher(sync.SourceConfig{
		URI: source.URL + "/flags", AuthHeader: "Bearer token", Headers: map[string]string{"x-custom": "value"},
	}, logger.NewLogger(nil, false))

	_, err := f.Fetch(context.Background(), "")
	require.NoError(t, err)
	h := source.lastRequest()
	require.Equal(t, "Bearer token", h.Get("Authorization"))
	require.Equal(t, "value", h.Get("X-Custom"))
	require.Equal(t, []string{"application/json", "application/yaml"}, h.Values("Accept"))
}
