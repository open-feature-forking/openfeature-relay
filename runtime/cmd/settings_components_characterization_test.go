package cmd

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/runtime"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

const (
	flagEvaluation = "flag-evaluation"
	ofrep          = "ofrep"
	flagSync       = "flag-sync"
)

// setting is the component set of one setting of flagd-full (UC-010 Q9). A setting has a name on the command line, in
// the FLAGD_ environment and in the configuration file, and a component set in each.
type setting struct {
	// field is the field of runtime.Config the setting reaches; empty for settings that Config does not carry.
	field string
	// endpoints are the endpoint types whose settings the field changes: the components the setting belongs to.
	endpoints []string
	// own names the setting's owner when no endpoint is the whole of it: the runtime, or the telemetry provider.
	own string
}

var allEndpoints = []string{flagEvaluation, flagSync, ofrep}

// flagdFullSettings is every flag of flagd start with the components it belongs to. A new flag has no entry here and
// fails TestEveryFlagdFullSettingHasAComponent, so that whoever adds it decides which components it belongs to.
var flagdFullSettings = map[string]setting{
	"management-port":                  {field: "ManagementPort", endpoints: []string{flagEvaluation}, own: "runtime"},
	"port":                             {field: "ServicePort", endpoints: []string{flagEvaluation}},
	"sync-port":                        {field: "SyncServicePort", endpoints: []string{flagSync}},
	"ofrep-port":                       {field: "OfrepServicePort", endpoints: []string{ofrep}},
	"sync-http-enabled":                {field: "SyncServiceHTTPEnabled", endpoints: []string{flagSync}},
	"ofrep-sse-enabled":                {field: "OfrepSSEEnabled", endpoints: []string{ofrep}},
	"ofrep-sse-inactivity-delay":       {field: "OfrepSSEInactivityDel", endpoints: []string{ofrep}},
	"ofrep-sse-public-url":             {field: "OfrepSSEPublicURL", endpoints: []string{ofrep}},
	"socket-path":                      {field: "ServiceSocketPath", endpoints: []string{flagEvaluation}},
	"sync-socket-path":                 {field: "SyncServiceSocketPath", endpoints: []string{flagSync}},
	"server-cert-path":                 {field: "ServiceCertPath", endpoints: []string{flagEvaluation, flagSync}},
	"server-key-path":                  {field: "ServiceKeyPath", endpoints: []string{flagEvaluation, flagSync}},
	"cors-origin":                      {field: "CORS", endpoints: allEndpoints},
	"context-value":                    {field: "ContextValues", endpoints: allEndpoints},
	"context-from-header":              {field: "HeaderToContextKeyMappings", endpoints: []string{flagEvaluation, ofrep}},
	"stream-deadline":                  {field: "StreamDeadline", endpoints: []string{flagEvaluation, flagSync}},
	"disable-sync-metadata":            {field: "DisableSyncMetadata", endpoints: []string{flagSync}},
	"max-request-body":                 {field: "MaxRequestBodyBytes", endpoints: allEndpoints},
	"max-request-header":               {field: "MaxRequestHeaderBytes", endpoints: allEndpoints},
	"strict-numeric-range":             {field: "StrictNumericRange", own: "runtime"},
	"keep-alive-min-time":              {field: "KeepAliveMinTime", own: "runtime"},
	"keep-alive-permit-without-stream": {field: "KeepAlivePermitWithoutStream", own: "runtime"},
	"uri":                              {field: "SyncProviders", own: "runtime"},
	"sources":                          {field: "SyncProviders", own: "runtime"},
	"log-format":                       {own: "runtime"},
	"debug":                            {own: "runtime"},
	"config":                           {own: "runtime"},
	"metrics-exporter":                 {own: "telemetry"},
	"otel-collector-uri":               {own: "telemetry"},
	"otel-cert-path":                   {own: "telemetry"},
	"otel-key-path":                    {own: "telemetry"},
	"otel-ca-path":                     {own: "telemetry"},
	"otel-reload-interval":             {own: "telemetry"},
}

// recorder is an endpoint factory that keeps the settings the runtime hands it.
type recorder struct {
	name string
	mu   *sync.Mutex
	seen map[string]map[string]any
}

func (r recorder) Type() string         { return r.name }
func (r recorder) Needs() service.Needs { return service.Needs{} }
func (r recorder) Create(s service.Settings) (service.Endpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen[r.name] = s.Config
	return nil, nil
}

// endpointSettingsOf builds a runtime from cfg with one recording endpoint per official type and returns the settings
// each received.
func endpointSettingsOf(t *testing.T, cfg runtime.Config) map[string]map[string]any {
	t.Helper()
	seen := map[string]map[string]any{}
	mu := &sync.Mutex{}
	var factories []service.Factory
	for _, name := range allEndpoints {
		factories = append(factories, recorder{name: name, mu: mu, seen: seen})
	}
	_, err := runtime.FromConfig(logger.NewLogger(nil, false), "test", cfg, distribution.Components{Endpoints: factories})
	require.NoError(t, err)
	return seen
}

// changed sets the field of cfg to a value other than its zero value.
func changed(t *testing.T, cfg *runtime.Config, field string) {
	t.Helper()
	f := reflect.ValueOf(cfg).Elem().FieldByName(field)
	require.True(t, f.IsValid(), "runtime.Config has no field %s", field)
	switch {
	case f.Type() == reflect.TypeFor[time.Duration]():
		f.SetInt(int64(7 * time.Second))
	case f.Kind() == reflect.Bool:
		f.SetBool(true)
	case f.CanInt():
		f.SetInt(7)
	case f.CanUint():
		f.SetUint(7)
	case f.Kind() == reflect.String:
		f.SetString("x")
	case f.Type() == reflect.TypeFor[[]string]():
		f.Set(reflect.ValueOf([]string{"x"}))
	case f.Type() == reflect.TypeFor[map[string]any]():
		f.Set(reflect.ValueOf(map[string]any{"k": "v"}))
	case f.Type() == reflect.TypeFor[map[string]string]():
		f.Set(reflect.ValueOf(map[string]string{"k": "v"}))
	default:
		t.Skipf("field %s of type %s is not exercised here", field, f.Type())
	}
}

// TestEveryFlagdFullSettingHasAComponent pins that each setting of flagd-full belongs to a set of components or to the
// runtime, and that the set equals the endpoint types whose settings the runtime derives from it. A flag stays a
// setting of the same components in the environment and the configuration file, which share its name.
func TestEveryFlagdFullSettingHasAComponent(t *testing.T) {
	var flags []string
	collect := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) { flags = append(flags, f.Name) })
	}
	collect(startCmd.Flags())
	collect(rootCmd.PersistentFlags())
	sort.Strings(flags)

	var known []string
	for name := range flagdFullSettings {
		known = append(known, name)
	}
	sort.Strings(known)
	require.Equal(t, known, flags, "every flag needs an entry in flagdFullSettings, and every entry a flag")

	base := endpointSettingsOf(t, runtime.Config{})
	for _, name := range known {
		s := flagdFullSettings[name]
		t.Run(name, func(t *testing.T) {
			var derived []string
			if s.field != "" {
				cfg := runtime.Config{}
				changed(t, &cfg, s.field)
				got := endpointSettingsOf(t, cfg)
				for _, endpoint := range allEndpoints {
					if !reflect.DeepEqual(base[endpoint], got[endpoint]) {
						derived = append(derived, endpoint)
					}
				}
			}
			sort.Strings(derived)
			want := append([]string(nil), s.endpoints...)
			sort.Strings(want)
			require.Equal(t, want, derived, "the endpoint types that receive %s", name)
			require.True(t, len(want) > 0 || s.own != "", fmt.Sprintf("%s belongs to no component and to no owner", name))
		})
	}
}
