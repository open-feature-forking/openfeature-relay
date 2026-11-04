package azblob

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
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

	// accountKey is a base64 key for a made-up account, long enough for HMAC under FIPS 140-only mode (112 bits)
	accountKey = "dGVzdC1hY2NvdW50LWtleS0t"
	// connectionString is a well-formed connection string for a made-up account
	connectionString = "DefaultEndpointsProtocol=https;AccountName=acct;AccountKey=dGVzdC1hY2NvdW50LWtleS0t;EndpointSuffix=core.windows.net"
)

// request is what the stub saw of one request.
type request struct {
	method, path, query, authorization, ifMatch string
}

// stub is the Blob service of the account "acct" with one blob, "container/flags.json".
type stub struct {
	mu       gosync.Mutex
	version  int
	content  string
	status   int // when not zero, every request answers with it
	requests []request
	server   *httptest.Server
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

func (s *stub) etag() string { return `"0x` + strconv.Itoa(s.version) + `"` }

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, request{
		r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("If-Match"),
	})
	fail := func(status int, code string) {
		w.Header().Set("x-ms-error-code", code)
		w.WriteHeader(status)
	}
	if s.status != 0 {
		fail(s.status, "ServerBusy")
		return
	}
	if r.URL.Path != "/acct/container/flags.json" {
		fail(http.StatusNotFound, "BlobNotFound")
		return
	}
	if match := r.Header.Get("If-Match"); match != "" && match != s.etag() {
		fail(http.StatusPreconditionFailed, "ConditionNotMet")
		return
	}
	w.Header().Set("ETag", s.etag())
	w.Header().Set("Last-Modified", "Fri, 02 Jan 2026 03:04:05 GMT")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(s.content)))
	w.Header().Set("x-ms-blob-type", "BlockBlob")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(s.content))
}

// startStub points the Azure environment at a stub of the Blob service with a shared key.
func startStub(t *testing.T) *stub {
	t.Helper()
	s := &stub{}
	s.set(flagsA)
	s.server = httptest.NewServer(s)
	t.Cleanup(s.server.Close)
	u, err := url.Parse(s.server.URL)
	require.NoError(t, err)

	t.Setenv("AZURE_STORAGE_ACCOUNT", "acct")
	t.Setenv("AZURE_STORAGE_KEY", accountKey)
	t.Setenv("AZURE_STORAGE_DOMAIN", u.Host)
	t.Setenv("AZURE_STORAGE_PROTOCOL", "http")
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

func TestAzblobConfig(t *testing.T) {
	t.Setenv("AZURE_STORAGE_ACCOUNT", "myaccount")
	tests := []struct {
		name           string
		uri            string
		expectedBucket string
		expectedObject string
	}{
		{
			name:           "simple path",
			uri:            "azblob://bucket/path/to/object",
			expectedBucket: "azblob://bucket/",
			expectedObject: "path/to/object",
		},
		{
			name:           "no object set", // the fetcher fails Init
			uri:            "azblob://bucket/",
			expectedBucket: "azblob://bucket/",
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
		Source: sync.SourceConfig{URI: "azblob://container/flags.json", Provider: Provider},
		Change: func(testing.TB) { s.set(flagsB) },
		Break:  func(testing.TB) { s.fail(http.StatusForbidden) },
	})
}

func TestFetch(t *testing.T) {
	s := startStub(t)
	fetcher := newFetcher(t, "azblob://container/flags.json")
	ctx := context.Background()
	require.NoError(t, fetcher.Init(ctx))

	first, err := fetcher.Fetch(ctx, "")
	require.NoError(t, err)
	require.Equal(t, flagsA, string(first.Data))
	require.Equal(t, ".json", first.Extension)
	require.Equal(t, "application/json", first.ContentType)

	t.Run("the same ETag is unchanged and is not read", func(t *testing.T) {
		before := len(s.seen())
		res, err := fetcher.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.True(t, res.Unchanged)
		seen := s.seen()[before:]
		require.Len(t, seen, 1)
		require.Equal(t, http.MethodHead, seen[0].method)
	})

	t.Run("a changed blob is read with the ETag that was compared", func(t *testing.T) {
		s.set(flagsB)
		res, err := fetcher.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.False(t, res.Unchanged)
		require.Equal(t, flagsB, string(res.Data))
		seen := s.seen()
		require.Equal(t, `"0x2"`, seen[len(seen)-1].ifMatch)
	})

	t.Run("a missing blob is a failed fetch", func(t *testing.T) {
		missing := newFetcher(t, "azblob://container/missing.json")
		_, err := missing.Fetch(ctx, "")
		require.ErrorContains(t, err, "couldn't get object attributes")
	})
}

func TestCredentialIsChosenFromTheEnvironment(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		wantAuth func(t *testing.T, r request)
	}{
		{
			name: "the shared key of the account",
			env:  map[string]string{"AZURE_STORAGE_KEY": accountKey},
			wantAuth: func(t *testing.T, r request) {
				require.True(t, strings.HasPrefix(r.authorization, "SharedKey acct:"), r.authorization)
			},
		},
		{
			name: "a SAS token goes in the query",
			env:  map[string]string{"AZURE_STORAGE_KEY": "", "AZURE_STORAGE_SAS_TOKEN": "sv=2022&sig=abc"},
			wantAuth: func(t *testing.T, r request) {
				require.Empty(t, r.authorization)
				require.Contains(t, r.query, "sig=abc")
			},
		},
		{
			name: "a connection string",
			env:  map[string]string{"AZURE_STORAGE_KEY": "", "AZURE_STORAGE_CONNECTION_STRING": "{{conn}}"},
			wantAuth: func(t *testing.T, r request) {
				require.True(t, strings.HasPrefix(r.authorization, "SharedKey acct:"), r.authorization)
			},
		},
		{
			name: "the other connection string variable",
			env:  map[string]string{"AZURE_STORAGE_KEY": "", "AZURE_STORAGEBLOB_CONNECTIONSTRING": "{{conn}}"},
			wantAuth: func(t *testing.T, r request) {
				require.True(t, strings.HasPrefix(r.authorization, "SharedKey acct:"), r.authorization)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := startStub(t)
			endpoint := s.server.URL + "/acct"
			for k, v := range tt.env {
				t.Setenv(k, strings.ReplaceAll(v, "{{conn}}",
					"BlobEndpoint="+endpoint+";AccountName=acct;AccountKey="+accountKey))
			}
			fetcher := newFetcher(t, "azblob://container/flags.json")
			_, err := fetcher.Fetch(context.Background(), "")
			require.NoError(t, err)

			seen := s.seen()
			require.NotEmpty(t, seen)
			tt.wantAuth(t, seen[0])
		})
	}
}

func TestUrlOptionsOverrideTheEnvironment(t *testing.T) {
	s := startStub(t)
	u, err := url.Parse(s.server.URL)
	require.NoError(t, err)
	// the environment points at a domain that nobody listens on; the source's query points at the stub
	t.Setenv("AZURE_STORAGE_DOMAIN", "127.0.0.1:1")
	t.Setenv("AZURE_STORAGE_PROTOCOL", "https")

	fetcher := newFetcher(t, "azblob://container/flags.json?domain="+u.Host+"&protocol=http&storage_account=acct")
	res, err := fetcher.Fetch(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, flagsA, string(res.Data))
}

func TestLocalEmulatorAndCDNServiceURLs(t *testing.T) {
	tests := []struct {
		name string
		opts serviceURLOptions
		want string
	}{
		{"public", serviceURLOptions{accountName: "acct"}, "https://acct.blob.core.windows.net"},
		{"another domain", serviceURLOptions{accountName: "acct", domain: "blob.example.com", protocol: "http"}, "http://acct.blob.example.com"},
		{"cdn", serviceURLOptions{accountName: "acct", domain: "cdn.example.com", isCDN: true}, "https://cdn.example.com"},
		{"emulator", serviceURLOptions{accountName: "acct", domain: "127.0.0.1:10000", protocol: "http"}, "http://127.0.0.1:10000/acct"},
		{"emulator by option", serviceURLOptions{accountName: "acct", domain: "host:10000", isLocalEmulator: true}, "https://host:10000/acct"},
		{"sas token", serviceURLOptions{accountName: "acct", sasToken: "sv=1&sig=x"}, "https://acct.blob.core.windows.net?sv=1&sig=x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.opts.serviceURL()
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	_, err := serviceURLOptions{accountName: "acct", protocol: "ftp"}.serviceURL()
	require.EqualError(t, err, `invalid protocol "ftp"`)
}

// TestEnvironmentAndOptions pins which environment variables and URL options are accepted, and how the others fail.
func TestEnvironmentAndOptions(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		uri  string
		want string // empty: accepted
	}{
		{"no account", map[string]string{"AZURE_STORAGE_ACCOUNT": ""}, "azblob://bucket/o", "environment variable AZURE_STORAGE_ACCOUNT not set or is blank"},
		{"account only: default credential", map[string]string{}, "azblob://bucket/o", ""},
		{"shared key", map[string]string{"AZURE_STORAGE_KEY": "dGVzdC1hY2NvdW50LWtleS0t"}, "azblob://bucket/o", ""},
		{
			"a shared key that is not base64 fails", map[string]string{"AZURE_STORAGE_KEY": "!!"}, "azblob://bucket/o",
			"failed azblob.NewSharedKeyCredential: decode account key: illegal base64 data at input byte 0",
		},
		{"sas token", map[string]string{"AZURE_STORAGE_SAS_TOKEN": "sv=2022&sig=abc"}, "azblob://bucket/o", ""},
		{"connection string", map[string]string{"AZURE_STORAGE_CONNECTION_STRING": connectionString}, "azblob://bucket/o", ""},
		{"the other connection string variable", map[string]string{"AZURE_STORAGEBLOB_CONNECTIONSTRING": connectionString}, "azblob://bucket/o", ""},
		{
			"a connection string that cannot be parsed fails", map[string]string{"AZURE_STORAGE_CONNECTION_STRING": "garbage"},
			"azblob://bucket/o", "connection string is either blank or malformed.",
		},
		{
			"domain, protocol, cdn and emulator variables",
			map[string]string{
				"AZURE_STORAGE_DOMAIN": "blob.example.com", "AZURE_STORAGE_PROTOCOL": "http",
				"AZURE_STORAGE_IS_CDN": "true", "AZURE_STORAGE_IS_LOCAL_EMULATOR": "true",
			},
			"azblob://bucket/o", "",
		},
		{"url options", map[string]string{}, "azblob://bucket/o?domain=blob.example.com&protocol=http&cdn=true&localemu=true", ""},
		{"the account in the url", map[string]string{}, "azblob://bucket/o?storage_account=other", ""},
		{"an invalid cdn value", map[string]string{}, "azblob://bucket/o?cdn=maybe", `strconv.ParseBool: parsing "maybe": invalid syntax`},
		{"an invalid protocol", map[string]string{}, "azblob://bucket/o?protocol=ftp", `invalid protocol "ftp"`},
		{"an unknown url option", map[string]string{}, "azblob://bucket/o?foo=bar", `unknown query parameter "foo"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{
				"AZURE_STORAGE_KEY", "AZURE_STORAGE_SAS_TOKEN", "AZURE_STORAGE_CONNECTION_STRING",
				"AZURE_STORAGEBLOB_CONNECTIONSTRING", "AZURE_STORAGE_DOMAIN", "AZURE_STORAGE_PROTOCOL",
				"AZURE_STORAGE_IS_CDN", "AZURE_STORAGE_IS_LOCAL_EMULATOR",
			} {
				t.Setenv(k, "")
			}
			t.Setenv("AZURE_STORAGE_ACCOUNT", "acct")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			_, err := NewFactory().CreateFetcher(sync.Settings{
				Source: sync.SourceConfig{URI: tt.uri}, Logger: logger.NewLogger(nil, false),
			})
			if tt.want != "" {
				require.Error(t, err)
				require.True(t, strings.HasPrefix(err.Error(), tt.want), "got %q, want a message starting with %q", err, tt.want)
				return
			}
			require.NoError(t, err)
		})
	}

	t.Run("a storage account with whitespace", func(t *testing.T) {
		t.Setenv("AZURE_STORAGE_ACCOUNT", "my account")
		_, err := NewFactory().CreateFetcher(sync.Settings{
			Source: sync.SourceConfig{URI: "azblob://bucket/o"}, Logger: logger.NewLogger(nil, false),
		})
		require.EqualError(t, err, "environment variable AZURE_STORAGE_ACCOUNT contains whitespace")
	})
}

func TestFactory(t *testing.T) {
	f := NewFactory()
	require.Equal(t, "azblob", f.Type())
	require.Equal(t, "blob", f.SourceType())
	require.Equal(t, []string{"azblob://"}, f.Schemes())

	got, ok := f.ParseURI("azblob://bucket/flags.yaml")
	require.True(t, ok)
	require.Equal(t, sync.SourceConfig{URI: "azblob://bucket/flags.yaml", Provider: "azblob"}, got)
	_, ok = f.ParseURI("file:/tmp/flags.json")
	require.False(t, ok)
	require.True(t, Matches("azblob://bucket/o"))
	require.False(t, Matches("s3://bucket/o"))
}
