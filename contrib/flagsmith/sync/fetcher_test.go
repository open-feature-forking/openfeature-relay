package flagsmith

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/fetching"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const (
	envKey      = "ser.test-environment-key-5b1c"
	envKeyVar   = "RELAY_TEST_FLAGSMITH_KEY"
	documentURL = "/api/v1/environment-document/"
)

// environment is a Flagsmith API the test controls: it serves pages of an environment document to the one key it
// knows.
type environment struct {
	*httptest.Server

	mu       gosync.Mutex
	pages    []string                     // the page bodies, in order
	status   int                          // when not 200, every request answers it
	relative bool                         // the Link header holds a relative URL
	link     func(r *http.Request) string // when set and not empty, the value of the Link header
	seen     []seenRequest
}

type seenRequest struct {
	path, query string
	header      http.Header
}

func page(overrides ...string) string {
	return fmt.Sprintf(`{"api_key":"client-key","name":"production","feature_states":[{"feature":{"name":"f"}}],"identity_overrides":[%s]}`,
		strings.Join(overrides, ","))
}

func override(identifier string) string {
	return fmt.Sprintf(`{"identifier":%q,"identity_features":[]}`, identifier)
}

func newEnvironment(t *testing.T, pages ...string) *environment {
	t.Helper()

	e := &environment{pages: pages, status: http.StatusOK}
	e.Server = httptest.NewServer(http.HandlerFunc(e.serve))
	t.Cleanup(e.Close)
	return e
}

func (e *environment) serve(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seen = append(e.seen, seenRequest{path: r.URL.Path, query: r.URL.RawQuery, header: r.Header.Clone()})

	if r.URL.Path != documentURL {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get(KeyHeader) != envKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if e.status != http.StatusOK {
		w.WriteHeader(e.status)
		return
	}
	index := 0
	if p := r.URL.Query().Get("page"); p != "" {
		_, _ = fmt.Sscanf(p, "%d", &index)
		index--
	}
	if index+1 < len(e.pages) {
		next := fmt.Sprintf("%s?page=%d", documentURL, index+2)
		if !e.relative {
			next = "http://" + r.Host + next
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, next))
	}
	if e.link != nil {
		if value := e.link(r); value != "" {
			w.Header().Set("Link", value)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(e.pages[index]))
}

func (e *environment) setLink(link func(r *http.Request) string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.link = link
}

func (e *environment) set(pages ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pages = pages
}

func (e *environment) fail(code int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status = code
}

func (e *environment) requests() []seenRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]seenRequest(nil), e.seen...)
}

// source is the configuration of a source of this environment, with its key read from the environment variable.
func (e *environment) source(t *testing.T) sync.SourceConfig {
	t.Helper()
	t.Setenv(envKeyVar, envKey)
	return sync.SourceConfig{
		URI:         e.URL + "/api/v1/",
		Provider:    Provider,
		HeadersFrom: map[string]sync.HeaderSource{KeyHeader: {Env: envKeyVar}},
	}
}

func fetcherFor(t *testing.T, source sync.SourceConfig, log *logger.Logger) sync.Fetcher {
	t.Helper()
	if log == nil {
		log = logger.NewLogger(nil, false)
	}
	fetcher, err := Factory{}.CreateFetcher(sync.Settings{Source: source, Logger: log})
	require.NoError(t, err)
	return fetcher
}

func TestFetcherContract(t *testing.T) {
	env := newEnvironment(t, page(override("a")))
	synctest.RunFetcher(t, Factory{}, synctest.FetcherInputs{
		Source: env.source(t),
		Change: func(testing.TB) { env.set(page(override("a"), override("b"))) },
		Break:  func(testing.TB) { env.fail(http.StatusInternalServerError) },
	})
}

func TestPagination(t *testing.T) {
	for _, relative := range []bool{false, true} {
		t.Run(fmt.Sprintf("relative link %v", relative), func(t *testing.T) {
			env := newEnvironment(t, page(override("a"), override("b")), page(override("c")), page(override("d"), override("e")))
			env.relative = relative

			result, err := fetcherFor(t, env.source(t), nil).Fetch(context.Background(), "")
			require.NoError(t, err)
			require.Equal(t, ".json", result.Extension)

			var document struct {
				Name      string `json:"name"`
				Overrides []struct {
					Identifier string `json:"identifier"`
				} `json:"identity_overrides"`
				FeatureStates []json.RawMessage `json:"feature_states"`
			}
			require.NoError(t, json.Unmarshal(result.Data, &document))
			require.Equal(t, "production", document.Name, "the other fields are those of the first page")
			require.Len(t, document.FeatureStates, 1)
			var identifiers []string
			for _, o := range document.Overrides {
				identifiers = append(identifiers, o.Identifier)
			}
			require.Equal(t, []string{"a", "b", "c", "d", "e"}, identifiers, "the overrides of every page, in order")

			requests := env.requests()
			require.Len(t, requests, 3)
			for _, r := range requests {
				require.Equal(t, envKey, r.header.Get(KeyHeader), "every page is requested with the key")
			}
			require.Equal(t, "page=2", requests[1].query)
		})
	}

	t.Run("a document without overrides on its first page gets those of the later ones", func(t *testing.T) {
		env := newEnvironment(t, `{"name":"production"}`, page(override("z")))
		result, err := fetcherFor(t, env.source(t), nil).Fetch(context.Background(), "")
		require.NoError(t, err)
		require.Contains(t, string(result.Data), `"identifier":"z"`)
		require.Contains(t, string(result.Data), `"name":"production"`)
	})
}

func TestUnchanged(t *testing.T) {
	env := newEnvironment(t, page(override("a")), page(override("b")))
	fetcher := fetcherFor(t, env.source(t), nil)
	ctx := context.Background()

	first, err := fetcher.Fetch(ctx, "")
	require.NoError(t, err)
	require.NotEmpty(t, first.Version)

	again, err := fetcher.Fetch(ctx, first.Version)
	require.NoError(t, err)
	require.True(t, again.Unchanged, "the same pages are not a change")
	require.Equal(t, first.Version, again.Version)

	full, err := fetcher.Fetch(ctx, "")
	require.NoError(t, err)
	require.False(t, full.Unchanged, "a fetch without a version is always a full one")
	require.JSONEq(t, string(first.Data), string(full.Data))

	t.Run("a change on a later page is a change", func(t *testing.T) {
		env.set(page(override("a")), page(override("b"), override("c")))
		changed, err := fetcher.Fetch(ctx, first.Version)
		require.NoError(t, err)
		require.False(t, changed.Unchanged)
		require.NotEqual(t, first.Version, changed.Version)
	})
}

func TestRefused(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			env := newEnvironment(t, page())
			env.fail(code)
			source := env.source(t)
			fetcher := fetcherFor(t, source, nil)

			result, err := fetcher.Fetch(context.Background(), "")
			require.ErrorIs(t, err, sync.ErrSourceRefused)
			require.Empty(t, result.Data)
			require.NotContains(t, err.Error(), envKey)
		})
	}

	t.Run("a wrong key", func(t *testing.T) {
		env := newEnvironment(t, page())
		source := env.source(t)
		t.Setenv(envKeyVar, "ser.some-other-key")
		_, err := fetcherFor(t, source, nil).Fetch(context.Background(), "")
		require.ErrorIs(t, err, sync.ErrSourceRefused)
	})

	t.Run("other failures are not refusals and are retried", func(t *testing.T) {
		for _, code := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusTooManyRequests, http.StatusNotFound} {
			env := newEnvironment(t, page())
			env.fail(code)
			_, err := fetcherFor(t, env.source(t), nil).Fetch(context.Background(), "")
			require.Error(t, err)
			require.NotErrorIs(t, err, sync.ErrSourceRefused, "status %d", code)
		}
	})
}

func TestProviderEndsOnRefusedKeyAtStart(t *testing.T) {
	env := newEnvironment(t, page())
	source := env.source(t)
	t.Setenv(envKeyVar, "ser.revoked")
	provider := fetching.New(fetching.Options{
		Fetcher:    fetcherFor(t, source, nil),
		Logger:     logger.NewLogger(nil, false),
		Source:     source.URI,
		SourceType: Provider,
		Interval:   5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, provider.Init(ctx))

	done := make(chan error, 1)
	go func() { done <- provider.Sync(ctx, make(chan sync.DataSync, 1)) }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, sync.ErrSourceRefused)
		require.ErrorContains(t, err, source.URI)
	case <-time.After(5 * time.Second):
		t.Fatal("the provider kept retrying a key the source refuses")
	}
}

func TestKeyNeverLogged(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	log := logger.NewLogger(zap.New(core), false)

	var texts []string
	record := func(err error) {
		if err != nil {
			texts = append(texts, err.Error())
		}
	}

	env := newEnvironment(t, page(override("a")), page(override("b")))
	fetcher := fetcherFor(t, env.source(t), log)
	_, err := fetcher.Fetch(context.Background(), "")
	require.NoError(t, err)

	env.fail(http.StatusUnauthorized)
	_, err = fetcher.Fetch(context.Background(), "")
	record(err)
	env.fail(http.StatusInternalServerError)
	_, err = fetcher.Fetch(context.Background(), "")
	record(err)
	env.set(`not json`)
	env.fail(http.StatusOK)
	_, err = fetcher.Fetch(context.Background(), "")
	record(err)

	gone := newEnvironment(t, page())
	source := gone.source(t)
	gone.Close()
	_, err = fetcherFor(t, source, log).Fetch(context.Background(), "")
	record(err)

	for _, entry := range logs.All() {
		text := entry.Message
		for key, field := range entry.ContextMap() {
			text += fmt.Sprintf(" %s=%v", key, field)
		}
		texts = append(texts, text)
	}
	require.Len(t, texts, 4+len(logs.All()), "every failure returned an error")
	for _, text := range texts {
		require.NotContains(t, text, envKey, "the key is in a log entry or an error")
	}
	require.NotEmpty(t, logs.All(), "the run logs something, so the scan means something")
}

// The key goes only to the address the source names: the pages of a document and redirects cannot take it elsewhere.
func TestKeyStaysOnTheSourcesAddress(t *testing.T) {
	var mu gosync.Mutex
	var elsewhere int
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		elsewhere++
		_, _ = w.Write([]byte(page()))
	}))
	t.Cleanup(other.Close)
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return elsewhere
	}

	t.Run("a next page on another address", func(t *testing.T) {
		env := newEnvironment(t, page(override("a")))
		env.setLink(func(*http.Request) string {
			return fmt.Sprintf(`<%s/api/v1/environment-document/?page=2>; rel="next"`, other.URL)
		})
		_, err := fetcherFor(t, env.source(t), nil).Fetch(context.Background(), "")
		require.ErrorContains(t, err, "another address")
		require.Zero(t, count())
	})

	t.Run("a redirect", func(t *testing.T) {
		redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
		}))
		t.Cleanup(redirecting.Close)
		t.Setenv(envKeyVar, envKey)
		source := sync.SourceConfig{
			URI:         redirecting.URL + "/api/v1/",
			Provider:    Provider,
			HeadersFrom: map[string]sync.HeaderSource{KeyHeader: {Env: envKeyVar}},
		}
		_, err := fetcherFor(t, source, nil).Fetch(context.Background(), "")
		require.Error(t, err)
		require.NotErrorIs(t, err, sync.ErrSourceRefused)
		require.Zero(t, count())
	})
}

func TestPagesThatLoopAreAnError(t *testing.T) {
	env := newEnvironment(t, page())
	env.setLink(func(r *http.Request) string {
		return fmt.Sprintf(`<http://%s%s>; rel="next"`, r.Host, r.URL.RequestURI())
	})
	_, err := fetcherFor(t, env.source(t), nil).Fetch(context.Background(), "")
	require.ErrorContains(t, err, "pages")
}

func TestBadPages(t *testing.T) {
	tests := map[string]string{
		"not json":         `<html>`,
		"not an object":    `[1,2]`,
		"overrides string": `{"identity_overrides":"x"}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			env := newEnvironment(t, body)
			result, err := fetcherFor(t, env.source(t), nil).Fetch(context.Background(), "")
			require.Error(t, err)
			require.Empty(t, result.Data)
			require.NotContains(t, err.Error(), body, "the page is not echoed")
		})
	}
}

func TestSourceNeedsTheKeyHeader(t *testing.T) {
	_, err := Factory{}.CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{URI: "https://edge.example/api/v1/", Provider: Provider},
		Logger: logger.NewLogger(nil, false),
	})
	require.ErrorContains(t, err, "https://edge.example/api/v1/")
	require.ErrorContains(t, err, KeyHeader)

	_, err = Factory{}.CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{
			URI: "https://edge.example/api/v1/", Provider: Provider,
			HeadersFrom: map[string]sync.HeaderSource{KeyHeader: {Env: "RELAY_TEST_FLAGSMITH_UNSET"}},
		},
		Logger: logger.NewLogger(nil, false),
	})
	require.ErrorContains(t, err, "RELAY_TEST_FLAGSMITH_UNSET", "an unset variable stops the source, naming the variable")

	t.Setenv(envKeyVar, envKey)
	_, err = Factory{}.CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{
			URI: "ftp://edge.example/", Provider: Provider,
			HeadersFrom: map[string]sync.HeaderSource{KeyHeader: {Env: envKeyVar}},
		},
		Logger: logger.NewLogger(nil, false),
	})
	require.ErrorContains(t, err, "http")
}

// The key is a credential: it comes from an environment variable or a file, never from the configuration (UC-019
// BR-008). A key written in headers stops the source, and the error never shows it.
func TestKeyInPlainHeadersIsRefused(t *testing.T) {
	tests := map[string]sync.SourceConfig{
		"in headers": {
			URI: "https://edge.example/api/v1/", Provider: Provider,
			Headers: map[string]string{KeyHeader: envKey},
		},
		"in headers, in another case": {
			URI: "https://edge.example/api/v1/", Provider: Provider,
			Headers: map[string]string{"x-environment-key": envKey},
		},
		"in headers and in headersFrom": {
			URI: "https://edge.example/api/v1/", Provider: Provider,
			Headers:     map[string]string{KeyHeader: envKey},
			HeadersFrom: map[string]sync.HeaderSource{KeyHeader: {Env: envKeyVar}},
		},
	}
	t.Setenv(envKeyVar, envKey)
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			fetcher, err := Factory{}.CreateFetcher(sync.Settings{Source: source, Logger: logger.NewLogger(nil, false)})
			require.Nil(t, fetcher)
			require.ErrorContains(t, err, "https://edge.example/api/v1/", "names the source")
			require.ErrorContains(t, err, KeyHeader, "names the header")
			require.ErrorContains(t, err, "headersFrom", "points to where the key goes")
			require.NotContains(t, err.Error(), envKey, "never the value")
		})
	}

	t.Run("other headers are still accepted next to the key", func(t *testing.T) {
		env := newEnvironment(t, page())
		source := env.source(t)
		source.Headers = map[string]string{"X-Team": "payments"}
		_, err := fetcherFor(t, source, nil).Fetch(context.Background(), "")
		require.NoError(t, err)
		require.Equal(t, "payments", env.requests()[0].header.Get("X-Team"))
	})
}

func TestSchemesAndURIs(t *testing.T) {
	require.NoError(t, sync.ValidateDeclarations(Factory{}))
	require.Equal(t, Provider, Factory{}.Type())

	for _, uri := range []string{"flagsmith+https://edge.example/api/v1/", "flagsmith+http://127.0.0.1:8000/api/v1/"} {
		source, claimed := Factory{}.ParseURI(uri)
		require.True(t, claimed, uri)
		require.Equal(t, Provider, source.Provider)
		require.Equal(t, uri, source.URI, "the source keeps the name it was given")
	}
	for _, uri := range []string{"https://edge.example/api/v1/", "http://x/", "file:/x"} {
		_, claimed := Factory{}.ParseURI(uri)
		require.False(t, claimed, "%s belongs to the http and file sync providers", uri)
	}

	t.Run("every form of the URI reads the same address", func(t *testing.T) {
		env := newEnvironment(t, page())
		t.Setenv(envKeyVar, envKey)
		for _, uri := range []string{
			env.URL + "/api/v1/",
			env.URL + "/api/v1",
			"flagsmith+" + env.URL + "/api/v1/",
		} {
			source := sync.SourceConfig{
				URI: uri, Provider: Provider,
				HeadersFrom: map[string]sync.HeaderSource{KeyHeader: {Env: envKeyVar}},
			}
			_, err := fetcherFor(t, source, nil).Fetch(context.Background(), "")
			require.NoError(t, err, uri)
		}
	})
}

func TestFetchAbandonsWhenContextIsDone(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(slow.Close)
	t.Cleanup(func() { close(release) })
	t.Setenv(envKeyVar, envKey)
	source := sync.SourceConfig{
		URI: slow.URL + "/api/v1/", Provider: Provider,
		HeadersFrom: map[string]sync.HeaderSource{KeyHeader: {Env: envKeyVar}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := fetcherFor(t, source, nil).Fetch(ctx, "")
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err)
		require.True(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the fetch did not stop when its context was done")
	}
}
