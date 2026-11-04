package http

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/fetching"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const waitFor = 5 * time.Second

func (s *flagSource) setStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = code
}

func (s *flagSource) requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

// provide runs the fetching sync provider over a fetcher and returns what it delivers and how Sync ended.
func provide(t *testing.T, fetcher sync.Fetcher, log *logger.Logger) (<-chan sync.DataSync, <-chan error) {
	t.Helper()

	provider := fetching.New(fetching.Options{
		Fetcher:    fetcher,
		Logger:     log,
		Source:     "test://http",
		SourceType: sync.SourceHTTP,
		Interval:   5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, provider.Init(ctx))
	deliveries := make(chan sync.DataSync, 8)
	done := make(chan error, 1)
	go func() { done <- provider.Sync(ctx, deliveries) }()
	return deliveries, done
}

// Today's flagd keeps retrying a source that answers 401 or 403, and delivers once the answer is a flag
// configuration.
func TestFetcherRetriesOn401And403(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			source := newFlagSource(t, `"e1"`, bodyA)
			source.setStatus(code)
			f := NewFetcher(sync.SourceConfig{URI: source.URL + "/flags"}, logger.NewLogger(nil, false))
			deliveries, done := provide(t, f, logger.NewLogger(nil, false))

			require.Eventually(t, func() bool { return source.requests() >= 2 }, waitFor, 5*time.Millisecond,
				"the provider asks again after a %d", code)
			select {
			case err := <-done:
				t.Fatalf("the provider ended on a %d: %v", code, err)
			case update := <-deliveries:
				t.Fatalf("delivered %q on a %d", update.FlagData, code)
			default:
			}

			source.setStatus(http.StatusOK)
			select {
			case update := <-deliveries:
				require.JSONEq(t, bodyA, update.FlagData)
			case err := <-done:
				t.Fatalf("the provider ended: %v", err)
			case <-time.After(waitFor):
				t.Fatal("nothing was delivered once the source answered")
			}
		})
	}
}

// What an operator puts into a header is never written to a log, whether the fetch works or fails and wherever the
// value comes from.
func TestStartupLogsDoNotContainHeaderValues(t *testing.T) {
	const (
		headerSecret = "header-secret-4f2a"
		authSecret   = "auth-secret-91bc"
		envSecret    = "env-secret-77e0"
		fileSecret   = "file-secret-0b6d"
	)
	t.Setenv("RELAY_TEST_LOG_KEY", envSecret)
	secretFile := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(secretFile, []byte(fileSecret), 0o600))

	tests := []struct {
		name   string
		config sync.SourceConfig
		sent   map[string]string
	}{
		{
			name: "headers and the authorization header",
			config: sync.SourceConfig{
				AuthHeader: "Bearer " + authSecret,
				Headers:    map[string]string{"X-Environment-Key": headerSecret},
			},
			sent: map[string]string{"X-Environment-Key": headerSecret, "Authorization": "Bearer " + authSecret},
		},
		{
			name: "headersFrom",
			config: sync.SourceConfig{
				HeadersFrom: map[string]sync.HeaderSource{
					"X-Environment-Key": {Env: "RELAY_TEST_LOG_KEY"},
					"X-Other-Key":       {File: secretFile},
				},
			},
			sent: map[string]string{"X-Environment-Key": envSecret, "X-Other-Key": fileSecret},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := newFlagSource(t, `"e1"`, bodyA)
			source.setStatus(http.StatusUnauthorized)

			core, logs := observer.New(zapcore.DebugLevel)
			log := logger.NewLogger(zap.New(core), false)
			tt.config.URI = source.URL + "/flags"
			fetcher, err := Factory{}.CreateFetcher(sync.Settings{Source: tt.config, Logger: log})
			require.NoError(t, err)
			deliveries, _ := provide(t, fetcher, log)

			require.Eventually(t, func() bool { return source.requests() >= 2 }, waitFor, 5*time.Millisecond)
			source.setStatus(http.StatusOK)
			select {
			case <-deliveries:
			case <-time.After(waitFor):
				t.Fatal("nothing was delivered")
			}

			require.NotEmpty(t, logs.All(), "the run logs something, so the scan means something")
			for _, entry := range logs.All() {
				text := entry.Message
				for key, field := range entry.ContextMap() {
					text += fmt.Sprintf(" %s=%v", key, field)
				}
				for _, value := range []string{headerSecret, authSecret, envSecret, fileSecret} {
					require.NotContains(t, text, value, "a log entry carries a header value")
				}
			}
			for name, value := range tt.sent {
				require.Equal(t, value, source.lastRequest().Get(name), "the header %s was sent", name)
			}
		})
	}
}

func TestHTTPHeadersFrom(t *testing.T) {
	t.Setenv("RELAY_TEST_HTTP_KEY", "key-from-env")
	secretFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(secretFile, []byte("Bearer token-from-file\n"), 0o600))

	source := newFlagSource(t, `"e1"`, bodyA)
	fetcher, err := Factory{}.CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{
			URI:     source.URL + "/flags",
			Headers: map[string]string{"X-Team": "payments", "x-environment-key": "from-headers"},
			HeadersFrom: map[string]sync.HeaderSource{
				"X-Environment-Key": {Env: "RELAY_TEST_HTTP_KEY"},
				"Authorization":     {File: secretFile},
			},
		},
		Logger: logger.NewLogger(nil, false),
	})
	require.NoError(t, err)

	result, err := fetcher.Fetch(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, bodyA, string(result.Data))
	seen := source.lastRequest()
	require.Equal(t, "key-from-env", seen.Get("X-Environment-Key"), "headersFrom wins over headers")
	require.Equal(t, "Bearer token-from-file", seen.Get("Authorization"))
	require.Equal(t, "payments", seen.Get("X-Team"), "headers still apply")
}

func TestHTTPHeadersFromMissingStopsCreation(t *testing.T) {
	_, err := Factory{}.CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{
			URI:         "https://flags.example/env-1",
			HeadersFrom: map[string]sync.HeaderSource{"X-Environment-Key": {Env: "RELAY_TEST_HTTP_MISSING"}},
		},
		Logger: logger.NewLogger(nil, false),
	})
	require.ErrorContains(t, err, "https://flags.example/env-1")
	require.ErrorContains(t, err, "X-Environment-Key")
	require.ErrorContains(t, err, "RELAY_TEST_HTTP_MISSING")
}
