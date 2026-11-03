package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	gosync "sync"
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

// request is what the stub saw of one request.
type request struct {
	method, path, authorization, ifMatch string
}

// stub is S3 with one object, "bucket/dir/flags.json", addressed by path.
type stub struct {
	mu       gosync.Mutex
	version  int
	content  string
	status   int // when not zero, every request answers with it
	requests []request
}

func (s *stub) set(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	s.content = content
}

func (s *stub) fail(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

func (s *stub) seen() []request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]request(nil), s.requests...)
}

func (s *stub) etag() string { return `"v` + strconv.Itoa(s.version) + `"` }

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, request{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("If-Match")})
	fail := func(status int, code string) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>` + code + `</Code><Message>` + code + `</Message></Error>`))
		}
	}
	if s.status != 0 {
		fail(s.status, "InternalError")
		return
	}
	if r.URL.Path != "/bucket/dir/flags.json" {
		fail(http.StatusNotFound, "NoSuchKey")
		return
	}
	if match := r.Header.Get("If-Match"); match != "" && match != s.etag() {
		fail(http.StatusPreconditionFailed, "PreconditionFailed")
		return
	}
	w.Header().Set("ETag", s.etag())
	w.Header().Set("Last-Modified", "Fri, 02 Jan 2026 03:04:05 GMT")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(s.content)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(s.content))
}

// startStub starts the stub and returns it with the options that make a source use it.
func startStub(t *testing.T) (*stub, string) {
	t.Helper()
	s := &stub{}
	s.set(flagsA)
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	t.Setenv("AWS_ACCESS_KEY_ID", "key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	return s, "?region=us-east-1&use_path_style=true&endpoint=" + server.URL
}

func newFetcher(t *testing.T, uri string) *blobfetch.Fetcher {
	t.Helper()
	f, err := NewFactory().CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{URI: uri}, Logger: logger.NewLogger(nil, false),
	})
	require.NoError(t, err)
	return f.(*blobfetch.Fetcher)
}

func TestS3Config(t *testing.T) {
	tests := []struct {
		name           string
		uri            string
		expectedBucket string
		expectedObject string
	}{
		{
			name:           "simple path",
			uri:            "s3://bucket/path/to/object",
			expectedBucket: "s3://bucket/",
			expectedObject: "path/to/object",
		},
		{
			name:           "options move to the bucket",
			uri:            "s3://bucket/path/to/object?region=eu-west-1",
			expectedBucket: "s3://bucket?region=eu-west-1",
			expectedObject: "path/to/object",
		},
		{
			name:           "no object set", // the fetcher fails Init
			uri:            "s3://bucket/",
			expectedBucket: "s3://bucket/",
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
	s, options := startStub(t)
	synctest.RunFetcher(t, NewFactory(), synctest.FetcherInputs{
		Source: sync.SourceConfig{URI: "s3://bucket/dir/flags.json" + options + "&anonymous=true", Provider: Provider},
		Change: func(testing.TB) { s.set(flagsB) },
		Break:  func(testing.TB) { s.fail(http.StatusForbidden) },
	})
}

func TestFetch(t *testing.T) {
	s, options := startStub(t)
	fetcher := newFetcher(t, "s3://bucket/dir/flags.json"+options)
	ctx := context.Background()
	require.NoError(t, fetcher.Init(ctx))

	first, err := fetcher.Fetch(ctx, "")
	require.NoError(t, err)
	require.Equal(t, flagsA, string(first.Data))
	require.Equal(t, ".json", first.Extension)
	require.Equal(t, "application/json", first.ContentType)
	require.NotEmpty(t, s.seen()[0].authorization, "requests are signed")

	t.Run("the same ETag is unchanged and is not read", func(t *testing.T) {
		before := len(s.seen())
		res, err := fetcher.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.True(t, res.Unchanged)
		seen := s.seen()[before:]
		require.Len(t, seen, 1)
		require.Equal(t, http.MethodHead, seen[0].method)
	})

	t.Run("a changed object is read with the ETag that was compared", func(t *testing.T) {
		s.set(flagsB)
		res, err := fetcher.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.False(t, res.Unchanged)
		require.Equal(t, flagsB, string(res.Data))
		seen := s.seen()
		require.Equal(t, `"v2"`, seen[len(seen)-1].ifMatch)
	})

	t.Run("a missing object is a failed fetch", func(t *testing.T) {
		missing := newFetcher(t, "s3://bucket/dir/missing.json"+options)
		_, err := missing.Fetch(ctx, "")
		require.ErrorContains(t, err, "couldn't get object attributes")
		require.ErrorContains(t, err, "NotFound")
	})
}

func TestAnonymousSourcesAreNotSigned(t *testing.T) {
	s, options := startStub(t)
	fetcher := newFetcher(t, "s3://bucket/dir/flags.json"+options+"&anonymous=true")
	_, err := fetcher.Fetch(context.Background(), "")
	require.NoError(t, err)
	for _, r := range s.seen() {
		require.Empty(t, r.authorization)
	}
}

func TestVirtualHostedAddressingMovesTheBucketIntoTheHost(t *testing.T) {
	s := &stub{}
	s.set(flagsA)
	server := httptest.NewServer(s)
	defer server.Close()
	t.Setenv("AWS_ACCESS_KEY_ID", "key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")

	// without use_path_style the bucket is a subdomain of the endpoint, which this stub does not serve; with
	// hostname_immutable the endpoint is used as it is
	fetcher := newFetcher(t, "s3://bucket/dir/flags.json?region=us-east-1&hostname_immutable=true&endpoint="+server.URL)
	res, err := fetcher.Fetch(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, flagsA, string(res.Data))
}

// TestQueryOptions pins which URL query options are accepted, and how the others fail.
func TestQueryOptions(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")

	tests := []struct {
		query   string
		wantErr string
	}{
		{query: ""},
		{query: "region=eu-west-1"},
		{query: "endpoint=http://127.0.0.1:1"},
		{query: "use_path_style=true"},
		{query: "s3ForcePathStyle=true"},
		{query: "disable_https=true"},
		{query: "dualstack=true"},
		{query: "fips=true"},
		{query: "accelerate=true"},
		{query: "anonymous=true"},
		{query: "hostname_immutable=true"},
		{query: "rate_limiter_capacity=10"},
		{query: "awssdk=v2"},
		// write-only options are accepted
		{query: "ssetype=aws:kms"},
		{query: "kmskeyid=key"},
		{query: "awssdk=v1", wantErr: `open bucket s3://bucket?awssdk=v1: unsupported value "v1" for "awssdk": only the AWS SDK v2 is supported`},
		{query: "ssetype=nope", wantErr: `"nope" is not a valid value for "ssetype"`},
		{query: "profile=nope", wantErr: "open bucket s3://bucket?profile=nope: failed to get shared config profile, nope"},
		{
			query:   "use_path_style=maybe",
			wantErr: `invalid value for "use_path_style": strconv.ParseBool: parsing "maybe": invalid syntax`,
		},
		{
			query:   "accelerate=maybe",
			wantErr: `invalid value for "accelerate": strconv.ParseBool: parsing "maybe": invalid syntax`,
		},
		// the names of the AWS SDK v1 path are not accepted
		{query: "s3_force_path_style=true", wantErr: `open bucket s3://bucket?s3_force_path_style=true: unknown query parameter "s3_force_path_style"`},
		{query: "disableSSL=true", wantErr: `open bucket s3://bucket?disableSSL=true: unknown query parameter "disableSSL"`},
		{query: "disable_ssl=true", wantErr: `open bucket s3://bucket?disable_ssl=true: unknown query parameter "disable_ssl"`},
		{query: "foo=bar", wantErr: `open bucket s3://bucket?foo=bar: unknown query parameter "foo"`},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			uri := "s3://bucket/flags.json"
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

func TestFactory(t *testing.T) {
	f := NewFactory()
	require.Equal(t, "s3", f.Type())
	require.Equal(t, "blob", f.SourceType())
	require.Equal(t, []string{"s3://"}, f.Schemes())

	got, ok := f.ParseURI("s3://bucket/flags.json?region=eu")
	require.True(t, ok)
	require.Equal(t, sync.SourceConfig{URI: "s3://bucket/flags.json?region=eu", Provider: "s3"}, got)
	_, ok = f.ParseURI("file:/tmp/flags.json")
	require.False(t, ok)
	require.True(t, Matches("s3://bucket/o"))
	require.False(t, Matches("gs://bucket/o"))
}
