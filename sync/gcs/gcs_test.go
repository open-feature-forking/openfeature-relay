package gcs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	gosync "sync"
	"sync/atomic"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/blobfetch"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
	"github.com/stretchr/testify/require"
)

const (
	flagsA = `{"flags":{"a":{}}}`
	flagsB = `{"flags":{"b":{}}}`
)

// stub is Cloud Storage's JSON API for one object, reached through STORAGE_EMULATOR_HOST.
type stub struct {
	mu         gosync.Mutex
	generation int64
	content    string
	status     int // when not zero, every request answers with it
	requests   []string
	downloads  []int64 // the generation each download asked for
}

func (s *stub) set(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.content = content
}

func (s *stub) fail(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

func (s *stub) calls() (requests []string, downloads []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...), append([]int64(nil), s.downloads...)
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r.Method+" "+r.URL.RequestURI())
	if s.status != 0 {
		http.Error(w, `{"error":{"code":`+http.StatusText(s.status)+`}}`, s.status)
		return
	}
	// the metadata of the object: /storage/v1/b/<bucket>/o/<object>
	if strings.HasPrefix(r.URL.Path, "/storage/v1/b/") && r.URL.Query().Get("alt") != "media" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "flags.json", "bucket": "bucket", "generation": strconv.FormatInt(s.generation, 10),
			"metageneration": "1", "contentType": "application/json", "updated": "2026-01-02T03:04:05Z",
			"size": strconv.Itoa(len(s.content)),
		})
		return
	}
	// the download of the object
	if g := r.URL.Query().Get("generation"); g != "" {
		n, _ := strconv.ParseInt(g, 10, 64)
		s.downloads = append(s.downloads, n)
	} else {
		s.downloads = append(s.downloads, 0)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Goog-Generation", strconv.FormatInt(s.generation, 10))
	_, _ = w.Write([]byte(s.content))
}

func startStub(t *testing.T) *stub {
	t.Helper()
	s := &stub{}
	s.set(flagsA)
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	t.Setenv("STORAGE_EMULATOR_HOST", u.Host)
	return s
}

func newFetcher(t *testing.T, uri string) *blobfetch.Fetcher {
	t.Helper()
	f, err := NewFactory().CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{URI: uri}, Logger: logger.NewLogger(nil, false),
	})
	require.NoError(t, err)
	return f.(*blobfetch.Fetcher)
}

func TestGcsConfig(t *testing.T) {
	tests := []struct {
		name           string
		uri            string
		expectedBucket string
		expectedObject string
	}{
		{
			name:           "simple path",
			uri:            "gs://bucket/path/to/object",
			expectedBucket: "gs://bucket/",
			expectedObject: "path/to/object",
		},
		{
			name:           "no object set", // the fetcher fails Init
			uri:            "gs://bucket/",
			expectedBucket: "gs://bucket/",
			expectedObject: "",
		},
		{
			name:           "malformed uri", // the fetcher fails Init
			uri:            "malformed",
			expectedBucket: "",
			expectedObject: "malformed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := newFetcher(t, tt.uri)
			require.Equal(t, tt.expectedBucket, fetcher.Bucket())
			require.Equal(t, tt.expectedObject, fetcher.Object())
		})
	}
}

func TestFetcherContract(t *testing.T) {
	s := startStub(t)
	synctest.RunFetcher(t, NewFactory(), synctest.FetcherInputs{
		Source: sync.SourceConfig{URI: "gs://bucket/flags.json?anonymous=true", Provider: Provider},
		Change: func(testing.TB) { s.set(flagsB) },
		Break:  func(testing.TB) { s.fail(http.StatusInternalServerError) },
	})
}

func TestFetch(t *testing.T) {
	s := startStub(t)
	fetcher := newFetcher(t, "gs://bucket/flags.json?anonymous=true")
	ctx := context.Background()
	require.NoError(t, fetcher.Init(ctx))

	first, err := fetcher.Fetch(ctx, "")
	require.NoError(t, err)
	require.Equal(t, flagsA, string(first.Data))
	require.Equal(t, ".json", first.Extension)
	require.Equal(t, "application/json", first.ContentType)

	t.Run("the same generation is unchanged and is not read", func(t *testing.T) {
		_, before := s.calls()
		res, err := fetcher.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.True(t, res.Unchanged)
		_, after := s.calls()
		require.Len(t, after, len(before))
	})

	t.Run("a changed object is read at the generation that was compared", func(t *testing.T) {
		s.set(flagsB)
		res, err := fetcher.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.False(t, res.Unchanged)
		require.Equal(t, flagsB, string(res.Data))
		_, downloads := s.calls()
		require.Equal(t, int64(2), downloads[len(downloads)-1])
	})

	t.Run("a missing object is a failed fetch", func(t *testing.T) {
		s.fail(http.StatusNotFound)
		_, err := fetcher.Fetch(ctx, "")
		require.ErrorContains(t, err, "couldn't get object attributes")
		require.ErrorContains(t, err, "object doesn't exist")
	})
}

func TestQueryOptions(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(keyPath, []byte("not a key"), 0o600))

	tests := []struct {
		query   string
		wantErr string
	}{
		{query: ""},
		{query: "anonymous=true"},
		{query: "anonymous=false"},
		{query: "access_id=id"},
		{query: "access_id=-"},
		{query: "access_id=id&anonymous=true"},
		{query: "private_key_path=" + keyPath},
		{
			query:   "anonymous=maybe",
			wantErr: `open bucket gs://bucket?anonymous=maybe: invalid value "maybe" for query parameter "anonymous": strconv.ParseBool: parsing "maybe": invalid syntax`,
		},
		{
			query:   "private_key_path=/nonexistent",
			wantErr: "open bucket gs://bucket?private_key_path=/nonexistent: open /nonexistent: no such file or directory",
		},
		{query: "foo=bar", wantErr: `open bucket gs://bucket?foo=bar: invalid query parameter "foo"`},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			uri := "gs://bucket/flags.json"
			if tt.query != "" {
				uri += "?" + tt.query
			}
			_, err := NewFactory().CreateFetcher(sync.Settings{
				Source: sync.SourceConfig{URI: uri}, Logger: logger.NewLogger(nil, false),
			})
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestAnonymousSourcesSendNoCredentials(t *testing.T) {
	var authorization []string
	var mu gosync.Mutex
	s := &stub{}
	s.set(flagsA)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authorization = append(authorization, r.Header.Get("Authorization"))
		mu.Unlock()
		s.ServeHTTP(w, r)
	}))
	defer server.Close()
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	t.Setenv("STORAGE_EMULATOR_HOST", u.Host)

	for _, query := range []string{"anonymous=true", "access_id=-"} {
		fetcher := newFetcher(t, "gs://bucket/flags.json?"+query)
		_, err := fetcher.Fetch(context.Background(), "")
		require.NoError(t, err, query)
	}
	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, authorization)
	for _, a := range authorization {
		require.Empty(t, a)
	}
}

func TestFactory(t *testing.T) {
	f := NewFactory()
	require.Equal(t, "gcs", f.Type())
	require.Equal(t, "blob", f.SourceType())
	require.Equal(t, []string{"gs://"}, f.Schemes())

	got, ok := f.ParseURI("gs://bucket/path/flags.json")
	require.True(t, ok)
	require.Equal(t, sync.SourceConfig{URI: "gs://bucket/path/flags.json", Provider: "gcs"}, got)
	_, ok = f.ParseURI("file:/tmp/flags.json")
	require.False(t, ok)
	require.True(t, Matches("gs://bucket/o"))
	require.False(t, Matches("s3://bucket/o"))
}

// TestClientIsCreatedOnceAndClosedOnShutdown: credentials are looked up when the first fetch creates the client, and
// the client with its token source is kept for every later fetch.
func TestClientIsCreatedOnceAndClosedOnShutdown(t *testing.T) {
	s := startStub(t)
	var created atomic.Int32
	f, err := factory(nil, func() { created.Add(1) }).CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{URI: "gs://bucket/flags.json?anonymous=true"}, Logger: logger.NewLogger(nil, false),
	})
	require.NoError(t, err)
	ctx := context.Background()
	require.Zero(t, created.Load(), "no client before the first fetch")

	first, err := f.Fetch(ctx, "")
	require.NoError(t, err)
	for range 3 {
		res, err := f.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.True(t, res.Unchanged)
	}
	s.set(flagsB)
	_, err = f.Fetch(ctx, first.Version)
	require.NoError(t, err)
	require.Equal(t, int32(1), created.Load(), "one client for all five fetches")

	require.NoError(t, f.(io.Closer).Close())
	_, err = f.Fetch(ctx, "")
	require.NoError(t, err)
	require.Equal(t, int32(2), created.Load(), "a closed client is not used again")
}

func TestFailedClientCreationIsTriedAgain(t *testing.T) {
	startStub(t)
	var created atomic.Int32
	// a credentials file that does not exist makes the first creation fail
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("STORAGE_EMULATOR_HOST", "")
	f, err := factory(nil, func() { created.Add(1) }).CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{URI: "gs://bucket/flags.json"}, Logger: logger.NewLogger(nil, false),
	})
	require.NoError(t, err, "missing credentials do not fail at startup")
	_, err = f.Fetch(context.Background(), "")
	require.Error(t, err)
	require.Zero(t, created.Load())
}
