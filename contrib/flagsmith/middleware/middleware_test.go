package flagsmithmiddleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware/middlewaretest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/stretchr/testify/require"

	flagsmithmiddleware "github.com/open-feature-forking/openfeature-relay/contrib/flagsmith/middleware"
)

const (
	clientKey = "ClientKeyOfDev"
	serverKey = "ser.ServerKeyOfProd"
)

func request(key string) func() *http.Request {
	return func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags", nil)
		if key != "" {
			r.Header.Set(flagsmithmiddleware.Header, key)
		}
		return r
	}
}

// settings sets the keys in the environment and returns the configuration that names them.
func settings(t *testing.T) map[string]any {
	t.Helper()
	t.Setenv("TEST_FLAGSMITH_CLIENT_KEY", clientKey)
	t.Setenv("TEST_FLAGSMITH_SERVER_KEY", serverKey)
	return map[string]any{flagsmithmiddleware.SettingEnvironments: []any{
		map[string]any{
			flagsmithmiddleware.SettingKey:     map[string]any{"env": "TEST_FLAGSMITH_CLIENT_KEY"},
			flagsmithmiddleware.SettingFlagSet: clientKey,
		},
		map[string]any{
			flagsmithmiddleware.SettingKey:     map[string]any{"env": "TEST_FLAGSMITH_SERVER_KEY"},
			flagsmithmiddleware.SettingFlagSet: "ClientKeyOfProd",
		},
	}}
}

func create(t *testing.T, config map[string]any) (middleware.Middleware, error) {
	t.Helper()
	return flagsmithmiddleware.Factory{}.Create(middleware.Settings{
		Logger: logger.NewLogger(nil, false), Telemetry: telemetry.Noop(), Config: config,
	})
}

func TestContract(t *testing.T) {
	middlewaretest.Run(t, flagsmithmiddleware.NewFactory(), middlewaretest.Inputs{
		Config:   settings(t),
		Passed:   request(clientKey),
		Answered: request("unknown"),
		Binds: &middlewaretest.Binding{
			Selector:   "flagSetId=" + clientKey,
			Attributes: map[string]any{flagsmithmiddleware.CredentialAttribute: flagsmithmiddleware.KindClient},
		},
		InvalidConfig: map[string]any{flagsmithmiddleware.SettingEnvironments: []any{
			map[string]any{flagsmithmiddleware.SettingKey: map[string]any{"env": "TEST_FLAGSMITH_CLIENT_KEY"}, flagsmithmiddleware.SettingFlagSet: "a=b"},
		}},
		FaultyValue: "a=b",
	})
}

// seen is what the wrapped handler sees of a request.
type seen struct {
	reached    bool
	flagSet    string
	bound      bool
	attributes map[string]any
}

func run(t *testing.T, mw middleware.Middleware, r *http.Request) (*httptest.ResponseRecorder, seen) {
	t.Helper()
	var s seen
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.reached = true
		selector, bound, err := middleware.BoundSelector(r.Context())
		require.NoError(t, err)
		s.bound, s.flagSet = bound, selector.FlagSetId()
		s.attributes = evaluator.AttributesFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec, s
}

func TestBindsFlagSetAndKind(t *testing.T) {
	mw, err := create(t, settings(t))
	require.NoError(t, err)

	for name, tt := range map[string]struct {
		key, flagSet, kind string
	}{
		"client key": {clientKey, clientKey, flagsmithmiddleware.KindClient},
		"server key": {serverKey, "ClientKeyOfProd", flagsmithmiddleware.KindServer},
	} {
		t.Run(name, func(t *testing.T) {
			rec, s := run(t, mw, request(tt.key)())

			require.Equal(t, http.StatusNoContent, rec.Code)
			require.True(t, s.bound)
			require.Equal(t, tt.flagSet, s.flagSet)
			require.Equal(t, map[string]any{flagsmithmiddleware.CredentialAttribute: tt.kind}, s.attributes)
		})
	}
}

// A refusal is a bare 401: no body, no challenge, and the wrapped handler is never reached.
func TestUnknownKey401(t *testing.T) {
	mw, err := create(t, settings(t))
	require.NoError(t, err)

	for name, key := range map[string]string{
		"missing":          "",
		"unknown":          "SomeOtherKey",
		"prefix of a key":  clientKey[:5],
		"a key plus extra": clientKey + "x",
		"other case":       "clientkeyofdev",
	} {
		t.Run(name, func(t *testing.T) {
			rec, s := run(t, mw, request(key)())

			require.Equal(t, http.StatusUnauthorized, rec.Code)
			require.Empty(t, rec.Body.String())
			require.Empty(t, rec.Header().Get("WWW-Authenticate"))
			require.False(t, s.reached)
		})
	}
}

// The key can come with the headers of any other credential; only X-Environment-Key counts.
func TestOtherHeadersDoNotAuthenticate(t *testing.T) {
	mw, err := create(t, settings(t))
	require.NoError(t, err)
	r := request("")()
	r.Header.Set("Authorization", "Api-Key "+clientKey)
	r.Header.Set("X-Api-Key", clientKey)

	rec, s := run(t, mw, r)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.False(t, s.reached)
}

// A client cannot widen the flag set its key is bound to with a selector of its own.
func TestClientCannotOverrideSelector(t *testing.T) {
	mw, err := create(t, settings(t))
	require.NoError(t, err)

	var ctx context.Context
	h := mw.Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() }))
	h.ServeHTTP(httptest.NewRecorder(), request(clientKey)())
	require.NotNil(t, ctx)

	_, err = middleware.EffectiveSelector(ctx, "flagSetId=ClientKeyOfProd")
	require.ErrorIs(t, err, middleware.ErrNoFlags)

	same, err := middleware.EffectiveSelector(ctx, "flagSetId="+clientKey)
	require.NoError(t, err)
	require.Equal(t, clientKey, same.FlagSetId())

	none, err := middleware.EffectiveSelector(ctx, "")
	require.NoError(t, err)
	require.Equal(t, clientKey, none.FlagSetId())
}

func TestKeyFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(path, []byte(serverKey+"\n"), 0o600))
	mw, err := create(t, map[string]any{flagsmithmiddleware.SettingEnvironments: []any{
		map[string]any{flagsmithmiddleware.SettingKey: map[string]any{"file": path}, flagsmithmiddleware.SettingFlagSet: "ClientKeyOfProd"},
	}})
	require.NoError(t, err)

	rec, s := run(t, mw, request(serverKey)())

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "ClientKeyOfProd", s.flagSet)
	require.Equal(t, flagsmithmiddleware.KindServer, s.attributes[flagsmithmiddleware.CredentialAttribute])
}

func TestInvalidSettingsAreNamedNeverTheKey(t *testing.T) {
	t.Setenv("TEST_FLAGSMITH_CLIENT_KEY", clientKey)
	t.Setenv("TEST_FLAGSMITH_EMPTY", "")
	empty := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.WriteFile(empty, []byte(" \n"), 0o600))
	missing := filepath.Join(t.TempDir(), "missing")

	entry := func(key any, flagSet any) map[string]any {
		return map[string]any{flagsmithmiddleware.SettingKey: key, flagsmithmiddleware.SettingFlagSet: flagSet}
	}
	env := map[string]any{"env": "TEST_FLAGSMITH_CLIENT_KEY"}
	list := func(entries ...any) map[string]any {
		return map[string]any{flagsmithmiddleware.SettingEnvironments: entries}
	}

	tests := map[string]struct {
		config map[string]any
		want   string
	}{
		"no environments":      {map[string]any{}, `"environments" is missing`},
		"empty list":           {list(), `"environments"`},
		"not a list":           {map[string]any{flagsmithmiddleware.SettingEnvironments: serverKey}, "string is not a list"},
		"unknown setting":      {map[string]any{"realm": "x"}, `"realm"`},
		"entry not a map":      {list(serverKey), "string is not a map"},
		"unknown field":        {list(map[string]any{"kind": "server", flagsmithmiddleware.SettingKey: env, flagsmithmiddleware.SettingFlagSet: "a"}), `"kind"`},
		"no flag set":          {list(entry(env, "")), `"flagSet"`},
		"flag set with a list": {list(entry(env, "a,b")), `"a,b"`},
		"literal key":          {list(entry("LiteralKeyValue", "a")), "not in the configuration"},
		"unknown reference":    {list(entry(map[string]any{"vault": "x"}, "a")), `"vault"`},
		"unset variable":       {list(entry(map[string]any{"env": "TEST_FLAGSMITH_UNSET"}, "a")), `"TEST_FLAGSMITH_UNSET"`},
		"empty variable":       {list(entry(map[string]any{"env": "TEST_FLAGSMITH_EMPTY"}, "a")), `"TEST_FLAGSMITH_EMPTY"`},
		"missing file":         {list(entry(map[string]any{"file": missing}, "a")), "cannot be read"},
		"empty file":           {list(entry(map[string]any{"file": empty}, "a")), "is empty"},
		"key listed twice":     {list(entry(env, "a"), entry(env, "b")), "entry 1 has the key of another entry"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := create(t, tt.config)

			require.ErrorContains(t, err, `"flagsmith-credential"`)
			require.ErrorContains(t, err, tt.want)
			require.NotContains(t, err.Error(), clientKey)
			require.NotContains(t, err.Error(), serverKey)
			require.NotContains(t, err.Error(), "LiteralKeyValue")
		})
	}
}

// counter records the reasons of the refusals the middleware counts through the injected telemetry provider.
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
	if name == flagsmithmiddleware.RefusedMetric {
		return m.c
	}
	return m.Meter.Int64Counter(name)
}

type provider struct {
	telemetry.Provider
	c *counter
}

func (p provider) Meter(scope string) telemetry.Meter {
	return meter{Meter: p.Provider.Meter(scope), c: p.c}
}

func TestRefusalsAreCountedThroughTheProvider(t *testing.T) {
	c := &counter{}
	mw, err := flagsmithmiddleware.Factory{}.Create(middleware.Settings{
		Logger: logger.NewLogger(nil, false), Telemetry: provider{Provider: telemetry.Noop(), c: c}, Config: settings(t),
	})
	require.NoError(t, err)

	run(t, mw, request("")())
	run(t, mw, request("wrong")())
	run(t, mw, request(clientKey)())

	require.Equal(t, []string{"missing", "unknown"}, c.reasons)
}

func TestNewFactoryMatchesType(t *testing.T) {
	require.Equal(t, flagsmithmiddleware.Type, flagsmithmiddleware.NewFactory().Type())
	require.NoError(t, middleware.ValidateDeclarations(flagsmithmiddleware.NewFactory()))
}
