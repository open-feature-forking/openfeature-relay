package middlewareexample_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware/middlewaretest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/stretchr/testify/require"

	middlewareexample "github.com/open-feature/flagd/test/middleware-example"
)

func request(key string) func() *http.Request {
	return func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags", strings.NewReader("{}"))
		if key != "" {
			r.Header.Set(middlewareexample.DefaultHeader, key)
		}
		return r
	}
}

var config = map[string]any{
	middlewareexample.SettingKeys: map[string]any{"team-a-key": "flagSetId=team-a", "all-key": ""},
}

func TestContract(t *testing.T) {
	middlewaretest.Run(t, middlewareexample.Factory{}, middlewaretest.Inputs{
		Config:   config,
		Passed:   request("team-a-key"),
		Answered: request("wrong-key"),
		Binds: &middlewaretest.Binding{
			Selector:   "flagSetId=team-a",
			Attributes: map[string]any{"credential": "api-key"},
		},
		InvalidConfig: map[string]any{middlewareexample.SettingKeys: map[string]any{"k": "team=a"}},
		FaultyValue:   "team=a",
	})
}

// A key bound to no selector passes with the credential attribute alone.
func TestKeyWithoutSelector(t *testing.T) {
	middlewaretest.Run(t, middlewareexample.Factory{}, middlewaretest.Inputs{
		Config:        config,
		Passed:        request("all-key"),
		Answered:      request(""),
		Binds:         &middlewaretest.Binding{Attributes: map[string]any{"credential": "api-key"}},
		InvalidConfig: map[string]any{"keyz": map[string]any{}},
		FaultyValue:   "keyz",
	})
}

// counter records the refusals the middleware counts through the injected telemetry provider.
type counter struct {
	mu      sync.Mutex
	reasons []string
}

func (c *counter) Add(_ context.Context, _ int64, attrs ...telemetry.Attribute) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range attrs {
		if a.Key == "reason" {
			c.reasons = append(c.reasons, a.Value.AsString())
		}
	}
}

type meter struct {
	telemetry.Meter
	c *counter
}

func (m meter) Int64Counter(name string, _ ...telemetry.InstrumentOption) telemetry.Int64Counter {
	if name == middlewareexample.RefusedMetric {
		return m.c
	}
	return m.Meter.Int64Counter(name)
}

type provider struct {
	telemetry.Provider
	c *counter
}

func (p provider) Meter(scope string) telemetry.Meter { return meter{Meter: p.Provider.Meter(scope), c: p.c} }

// UC-005 A1: a request without a known key is answered with 401, never reaches the endpoint, and is counted through
// the telemetry provider.
func TestRefusalsAnsweredAndCounted(t *testing.T) {
	c := &counter{}
	mw, err := middlewareexample.Factory{}.Create(middleware.Settings{
		Logger:    logger.NewLogger(nil, false),
		Telemetry: provider{Provider: telemetry.Noop(), c: c},
		Config:    config,
	})
	require.NoError(t, err)
	reached := false
	h := middleware.Chain([]middleware.Middleware{mw}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))

	for _, key := range []string{"", "wrong-key"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, request(key)())
		require.Equal(t, http.StatusUnauthorized, rec.Code, key)
		require.Equal(t, "ApiKey header=X-Api-Key", rec.Header().Get("WWW-Authenticate"))
	}
	require.False(t, reached)
	require.Equal(t, []string{"missing", "unknown"}, c.reasons)
}

func TestInvalidSettingsAreNamed(t *testing.T) {
	tests := map[string]struct {
		config map[string]any
		want   string
	}{
		"no keys":          {map[string]any{}, `"keys" is missing`},
		"keys not a map":   {map[string]any{middlewareexample.SettingKeys: "k"}, `"keys"`},
		"selector type":    {map[string]any{middlewareexample.SettingKeys: map[string]any{"k": 3}}, "3"},
		"invalid selector": {map[string]any{middlewareexample.SettingKeys: map[string]any{"k": "team=a"}}, `"team=a"`},
		"header":           {map[string]any{middlewareexample.SettingHeader: "", middlewareexample.SettingKeys: map[string]any{"k": ""}}, `"header"`},
		"unknown":          {map[string]any{"realm": "x", middlewareexample.SettingKeys: map[string]any{"k": ""}}, `"realm"`},
	}
	for name, tt := range tests {
		_, err := middlewareexample.Factory{}.Create(middleware.Settings{
			Logger: logger.NewLogger(nil, false), Telemetry: telemetry.Noop(), Config: tt.config,
		})
		require.ErrorContains(t, err, tt.want, name)
		require.ErrorContains(t, err, `"api-key"`, name)
	}
}

func TestCustomHeader(t *testing.T) {
	mw, err := middlewareexample.Factory{}.Create(middleware.Settings{
		Logger: logger.NewLogger(nil, false), Telemetry: telemetry.Noop(),
		Config: map[string]any{middlewareexample.SettingHeader: "X-Team-Key", middlewareexample.SettingKeys: map[string]any{"k": ""}},
	})
	require.NoError(t, err)
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Team-Key", "k")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	require.Equal(t, http.StatusNoContent, rec.Code)
}
