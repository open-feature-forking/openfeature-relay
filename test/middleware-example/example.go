// Package middlewareexample is an example middleware extension: an API key check. A request with a known key is
// bound to that key's selector and carries the request attribute credential=api-key; any other request is answered
// with 401 Unauthorized and never reaches the service endpoint. It shows what an Extension Author's component
// module contains, and imports only the core (no transport or telemetry SDK).
//
// A distribution lists it with its settings:
//
//	{"header": "X-Api-Key", "keys": {"key-for-team-a": "flagSetId=team-a", "key-for-all": ""}}
package middlewareexample

import (
	"crypto/subtle"
	"fmt"
	"net/http"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// Type is the type name of the example middleware.
const Type = "api-key"

// Settings of the example middleware.
const (
	// SettingHeader names the request header that carries the key; the default is DefaultHeader.
	SettingHeader = "header"
	// SettingKeys maps each known key to the selector expression its requests are bound to; an empty expression
	// binds no selector.
	SettingKeys = "keys"
)

// DefaultHeader is the request header that carries the key when the settings name none.
const DefaultHeader = "X-Api-Key"

// RefusedMetric counts the requests the middleware answered with 401.
const RefusedMetric = "middleware_example.refused"

// Factory creates the example middleware.
type Factory struct{}

// Type returns "api-key".
func (Factory) Type() string { return Type }

// Create reads the settings. An unknown setting, a key without a selector expression string, or an invalid
// expression is an error that names it.
func (Factory) Create(s middleware.Settings) (middleware.Middleware, error) {
	m := &Middleware{header: DefaultHeader}
	for key, value := range s.Config {
		switch key {
		case SettingHeader:
			h, ok := value.(string)
			if !ok || h == "" {
				return nil, fmt.Errorf("middleware %q: setting %q: %v is not a header name", Type, key, value)
			}
			m.header = h
		case SettingKeys:
			keys, ok := value.(map[string]any)
			if !ok || len(keys) == 0 {
				return nil, fmt.Errorf("middleware %q: setting %q: %v is not a map of keys to selectors", Type, key, value)
			}
			for k, v := range keys {
				expression, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf("middleware %q: setting %q: the selector of a key is %v, not a string", Type, key, v)
				}
				if _, err := store.NewSelector(expression); err != nil {
					return nil, fmt.Errorf("middleware %q: setting %q: invalid selector %q: %w", Type, key, expression, err)
				}
				m.keys = append(m.keys, apiKey{key: []byte(k), selector: expression})
			}
		default:
			return nil, fmt.Errorf("middleware %q: unknown setting %q", Type, key)
		}
	}
	if len(m.keys) == 0 {
		return nil, fmt.Errorf("middleware %q: setting %q is missing", Type, SettingKeys)
	}
	m.refused = s.Telemetry.Meter("middlewareexample").Int64Counter(RefusedMetric,
		telemetry.WithDescription("Measures the number of requests refused for a missing or unknown API key."),
		telemetry.WithUnit("{request}"))
	return m, nil
}

type apiKey struct {
	key      []byte
	selector string
}

// Middleware is the example API key middleware.
type Middleware struct {
	header  string
	keys    []apiKey
	refused telemetry.Int64Counter
}

// lookup returns the selector expression of a known key; it compares every key in constant time.
func (m *Middleware) lookup(presented string) (string, bool) {
	var (
		selector string
		found    bool
	)
	for _, k := range m.keys {
		if subtle.ConstantTimeCompare(k.key, []byte(presented)) == 1 {
			selector, found = k.selector, true
		}
	}
	return selector, found
}

// Handler passes a request with a known key on, bound to the key's selector, and answers any other with 401.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := r.Header.Get(m.header)
		selector, ok := m.lookup(presented)
		if presented == "" || !ok {
			reason := "unknown"
			if presented == "" {
				reason = "missing"
			}
			m.refused.Add(r.Context(), 1, telemetry.String("reason", reason))
			w.Header().Set("WWW-Authenticate", "ApiKey header="+m.header)
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		ctx := middleware.WithAttributes(r.Context(), map[string]any{"credential": "api-key"})
		if selector != "" {
			ctx = middleware.BindSelector(ctx, selector)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
