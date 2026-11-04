//go:build unix

package runtime

import (
	"errors"
	"fmt"
	flagdcomponents "github.com/open-feature/flagd/flagd/components"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/stretchr/testify/require"
)

// tagging is a middleware that records its name on the request, in the order the requests pass the chain.
type tagging string

func (t tagging) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Add("X-Seen", string(t))
		next.ServeHTTP(w, r)
	})
}

// taggingFactory creates a tagging middleware named by its configuration, and keeps the settings it got.
type taggingFactory struct {
	settings *middleware.Settings
	err      error
	name     string
}

func (f taggingFactory) Type() string { return f.name }

func (f taggingFactory) Create(s middleware.Settings) (middleware.Middleware, error) {
	if f.settings != nil {
		*f.settings = s
	}
	if f.err != nil {
		return nil, f.err
	}
	return tagging(s.Config["tag"].(string)), nil
}

// keyRequired is a middleware that refuses every request without the X-Key header, and counts the requests it saw.
type keyRequired struct{ seen *atomic.Int64 }

func (k keyRequired) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k.seen.Add(1)
		if r.Header.Get("X-Key") == "" {
			http.Error(w, "key required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type keyRequiredFactory struct{ seen *atomic.Int64 }

func (keyRequiredFactory) Type() string { return "key-required" }
func (f keyRequiredFactory) Create(middleware.Settings) (middleware.Middleware, error) {
	return keyRequired{f.seen}, nil
}

// TestMiddlewareOrder pins that the service endpoints get one HTTP middleware that is the chain of the components'
// middleware in list order, built from each one's own configuration and the runtime's telemetry provider. Without
// middleware, as in flagd-full, they get none.
func TestMiddlewareOrder(t *testing.T) {
	var first, second middleware.Settings
	var endpointSettings service.Settings

	rt, err := FromConfig(logger.NewLogger(nil, false), "test", Config{SyncProviders: oneFileSource(t)}, withFlagd(Components{
		Endpoints: []service.Factory{capturingEndpoint{settings: &endpointSettings}},
		Middleware: []middleware.Configured{
			{Factory: taggingFactory{name: "first", settings: &first}, Config: map[string]any{"tag": "first"}},
			{Factory: taggingFactory{name: "second", settings: &second}, Config: map[string]any{"tag": "second"}},
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, rt)

	require.NotNil(t, endpointSettings.HTTPMiddleware)
	var seen []string
	h := endpointSettings.HTTPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.Header.Values("X-Seen")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, []string{"first", "second"}, seen, "the first middleware of the list sees a request first")

	require.Equal(t, map[string]any{"tag": "first"}, first.Config)
	require.Equal(t, map[string]any{"tag": "second"}, second.Config)
	require.NotNil(t, first.Logger)
	require.NotNil(t, first.Telemetry)

	t.Run("none", func(t *testing.T) {
		var settings service.Settings
		_, err := FromConfig(logger.NewLogger(nil, false), "test", Config{SyncProviders: oneFileSource(t)}, withFlagd(Components{
			Endpoints: []service.Factory{capturingEndpoint{settings: &settings}},
		}))
		require.NoError(t, err)
		require.Nil(t, settings.HTTPMiddleware, "the endpoints are left unwrapped")
	})
}

// TestPreflightBeforeAnyMiddleware pins that a middleware that refuses requests without a key, listed first, never
// sees a CORS preflight, on any official endpoint: CORS is a layer of the endpoint outside the chain. A request
// that is no preflight reaches it on every endpoint, and passes with a key.
func TestPreflightBeforeAnyMiddleware(t *testing.T) {
	var seen atomic.Int64
	p := newPorts(t)
	cfg := characterizedConfig(t, p)
	cfg.CORS = []string{"*"}
	components := flagdcomponents.Components()
	components.Middleware = []middleware.Configured{{Factory: keyRequiredFactory{seen: &seen}}}
	startRuntimeWith(t, cfg, components)

	endpoints := []struct{ name, url string }{
		{"flag evaluation", fmt.Sprintf("http://127.0.0.1:%d/flagd.evaluation.v1.Service/ResolveBoolean", p.service)},
		{"OFREP", fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/evaluate/flags/f1", p.ofrep)},
		{"flag sync", fmt.Sprintf("http://127.0.0.1:%d/flagd.sync.v1.FlagSyncService/FetchAllFlags", p.sync)},
	}
	do := func(method, url, key string) *http.Response {
		req, err := http.NewRequest(method, url, strings.NewReader(`{"flagKey":"f1"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		if method == http.MethodOptions {
			req.Header.Set("Origin", "https://example.com")
			req.Header.Set("Access-Control-Request-Method", "POST")
		}
		if key != "" {
			req.Header.Set("X-Key", key)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		return resp
	}

	for _, e := range endpoints {
		t.Run(e.name, func(t *testing.T) {
			before := seen.Load()
			resp := do(http.MethodOptions, e.url, "")
			require.Less(t, resp.StatusCode, 300, "CORS answers the preflight")
			require.Equal(t, before, seen.Load(), "the preflight never reached the middleware")

			resp = do(http.MethodPost, e.url, "")
			require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "the middleware answers a request without a key")
			require.Equal(t, before+1, seen.Load())

			resp = do(http.MethodPost, e.url, "secret")
			require.Equal(t, http.StatusOK, resp.StatusCode, "a request with a key passes")
		})
	}
}

// TestInvalidMiddlewareConfigStops pins that a middleware factory that cannot create its middleware, or that
// declares nothing usable, stops FromConfig with an error that names the middleware and the faulty value.
func TestInvalidMiddlewareConfigStops(t *testing.T) {
	tests := []struct {
		name       string
		middleware []middleware.Configured
		want       []string
	}{
		{
			name: "the factory rejects its configuration",
			middleware: []middleware.Configured{
				{Factory: taggingFactory{name: "ok"}, Config: map[string]any{"tag": "x"}},
				{Factory: taggingFactory{name: "api-key", err: errors.New(`invalid "header": "" is not a header name`)}},
			},
			want: []string{`"api-key"`, `invalid "header"`},
		},
		{
			name:       "a nil factory",
			middleware: []middleware.Configured{{}},
			want:       []string{"position 0", "the factory is nil"},
		},
		{
			name:       "an empty type name",
			middleware: []middleware.Configured{{Factory: taggingFactory{name: " "}}},
			want:       []string{"position 0", "the type name is empty"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, err := FromConfig(logger.NewLogger(nil, false), "test", Config{SyncProviders: oneFileSource(t)}, withFlagd(Components{
				Middleware: tt.middleware,
			}))
			require.Error(t, err)
			require.Nil(t, rt)
			for _, want := range tt.want {
				require.ErrorContains(t, err, want)
			}
		})
	}
}
