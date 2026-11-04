// Package flagsmithmiddleware is the flagsmith-credential middleware: it reads the Flagsmith environment key of a
// request from X-Environment-Key, binds the request to the flag set of that environment and attaches the kind of
// the key as the request attribute flagsmith.credential. Any other request is answered with 401 Unauthorized and
// never reaches the service endpoint.
//
// A distribution lists it with one entry per environment. The key comes from the environment or a file, never
// from the configuration itself:
//
//	{"environments": [
//	    {"key": {"env": "FLAGSMITH_CLIENT_KEY"}, "flagSet": "ClientKeyOfTheEnvironment"},
//	    {"key": {"file": "/run/secrets/server-key"}, "flagSet": "ClientKeyOfTheEnvironment"}
//	]}
//
// The flag set of an environment is its client-side key, which is public by design; a server-side key (prefix
// "ser.") names the flag set of the environment it belongs to.
package flagsmithmiddleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// Type is the type name of the middleware.
const Type = "flagsmith-credential"

// Header is the request header that carries the environment key.
const Header = "X-Environment-Key"

// Settings of the middleware.
const (
	// SettingEnvironments lists the environments: each entry has a key (a secret reference) and a flagSet.
	SettingEnvironments = "environments"
	// SettingKey and SettingFlagSet are the fields of an entry.
	SettingKey     = "key"
	SettingFlagSet = "flagSet"
)

// Credential kinds, the values of the request attribute CredentialAttribute.
const (
	KindClient = "client"
	KindServer = "server"
)

// CredentialAttribute is the request attribute that carries the kind of the key.
const CredentialAttribute = "flagsmith.credential"

// serverKeyPrefix starts the key of a server-side environment.
const serverKeyPrefix = "ser."

// RefusedMetric counts the requests the middleware answered with 401.
const RefusedMetric = "flagsmith_credential.refused"

// Factory creates the flagsmith-credential middleware.
type Factory struct{}

// Type returns "flagsmith-credential".
func (Factory) Type() string { return Type }

// Create reads the settings. An unknown setting, an entry without a flag set, a key that cannot be resolved or is
// listed twice is an error that names the setting or the reference, never the key.
func (Factory) Create(s middleware.Settings) (middleware.Middleware, error) {
	m := &Middleware{}
	for name, value := range s.Config {
		if name != SettingEnvironments {
			return nil, fmt.Errorf("middleware %q: unknown setting %q", Type, name)
		}
		entries, ok := value.([]any)
		if !ok || len(entries) == 0 {
			return nil, fmt.Errorf("middleware %q: setting %q: a %T is not a list of environments", Type, name, value)
		}
		for i, e := range entries {
			env, err := parseEnvironment(e)
			if err != nil {
				return nil, fmt.Errorf("middleware %q: setting %q: entry %d: %w", Type, name, i, err)
			}
			for _, other := range m.environments {
				if subtle.ConstantTimeCompare(other.digest[:], env.digest[:]) == 1 {
					return nil, fmt.Errorf("middleware %q: setting %q: entry %d has the key of another entry", Type, name, i)
				}
			}
			m.environments = append(m.environments, env)
		}
	}
	if len(m.environments) == 0 {
		return nil, fmt.Errorf("middleware %q: setting %q is missing", Type, SettingEnvironments)
	}
	m.refused = s.Telemetry.Meter("flagsmithmiddleware").Int64Counter(RefusedMetric,
		telemetry.WithDescription("Measures the number of requests refused for a missing or unknown environment key."),
		telemetry.WithUnit("{request}"))
	return m, nil
}

func parseEnvironment(entry any) (environment, error) {
	fields, ok := entry.(map[string]any)
	if !ok {
		return environment{}, fmt.Errorf("a %T is not a map with %q and %q", entry, SettingKey, SettingFlagSet)
	}
	for name := range fields {
		if name != SettingKey && name != SettingFlagSet {
			return environment{}, fmt.Errorf("unknown field %q", name)
		}
	}
	flagSet, ok := fields[SettingFlagSet].(string)
	if !ok || flagSet == "" {
		return environment{}, fmt.Errorf("field %q: %v is not a flag set", SettingFlagSet, fields[SettingFlagSet])
	}
	if strings.ContainsAny(flagSet, ",=") {
		return environment{}, fmt.Errorf("field %q: %q contains a comma or an equals sign", SettingFlagSet, flagSet)
	}
	selector := "flagSetId=" + flagSet
	if _, err := store.NewSelector(selector); err != nil {
		return environment{}, fmt.Errorf("field %q: invalid flag set %q: %w", SettingFlagSet, flagSet, err)
	}
	key, err := resolveSecret(fields[SettingKey])
	if err != nil {
		return environment{}, fmt.Errorf("field %q: %w", SettingKey, err)
	}
	kind := KindClient
	if strings.HasPrefix(key, serverKeyPrefix) {
		kind = KindServer
	}
	return environment{digest: sha256.Sum256([]byte(key)), selector: selector, kind: kind}, nil
}

type environment struct {
	digest   [sha256.Size]byte
	selector string
	kind     string
}

// Middleware is the flagsmith-credential middleware.
type Middleware struct {
	environments []environment
	refused      telemetry.Int64Counter
}

// lookup returns the environment of a key. It compares SHA-256 digests of the keys, which have a fixed length, in
// constant time and against every environment, so that neither the length nor the position of a match shows.
func (m *Middleware) lookup(presented string) (environment, bool) {
	digest := sha256.Sum256([]byte(presented))
	var (
		found environment
		ok    bool
	)
	for _, e := range m.environments {
		if subtle.ConstantTimeCompare(e.digest[:], digest[:]) == 1 {
			found, ok = e, true
		}
	}
	return found, ok
}

// Handler passes a request with a known key on, bound to the flag set of the key's environment, and answers any
// other with a bare 401.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := r.Header.Get(Header)
		env, ok := m.lookup(presented)
		if presented == "" || !ok {
			reason := "unknown"
			if presented == "" {
				reason = "missing"
			}
			m.refused.Add(r.Context(), 1, telemetry.String("reason", reason))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		ctx := middleware.WithAttributes(r.Context(), map[string]any{CredentialAttribute: env.kind})
		ctx = middleware.BindSelector(ctx, env.selector)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// NewFactory returns the factory of the middleware, as a generated main of a distribution calls it.
func NewFactory() middleware.Factory { return Factory{} }
