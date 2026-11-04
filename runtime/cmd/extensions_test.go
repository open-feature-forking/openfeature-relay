package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/stretchr/testify/require"
)

type sinkFactory struct{ name string }

func (f sinkFactory) Type() string                                    { return f.name }
func (sinkFactory) Kinds() []events.Kind                              { return []events.Kind{events.KindEvaluation} }
func (sinkFactory) Create(eventsink.Settings) (eventsink.Sink, error) { return nil, nil }

type middlewareFactory struct{ name string }

func (f middlewareFactory) Type() string { return f.name }
func (middlewareFactory) Create(middleware.Settings) (middleware.Middleware, error) {
	return nil, nil
}

func contents(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func extensible() distribution.Components {
	return distribution.Components{
		EventSinkFactories:  []eventsink.Factory{sinkFactory{"webhook"}, sinkFactory{"audit"}},
		MiddlewareFactories: []middleware.Factory{middlewareFactory{"api-key"}, middlewareFactory{"rate-limit"}},
		Endpoints:           []service.Factory{typedEndpoint{"flag-evaluation"}, typedEndpoint{"example"}},
	}
}

func TestNoEventSinksConfiguredNothingSubscribes(t *testing.T) {
	c := extensible()
	for name, path := range map[string]string{
		"no file":           "",
		"a file without":    contents(t, "a.yaml", "port: 8013\n"),
		"an empty file":     contents(t, "b.yaml", ""),
		"empty event-sinks": contents(t, "c.yaml", "event-sinks: []\n"),
		"null event-sinks":  contents(t, "d.yaml", "event-sinks:\n"),
	} {
		got, err := loadExtensionSettings(c, path)
		require.NoError(t, err, name)
		require.Empty(t, got.components.EventSinks, name)
		require.Empty(t, got.components.Middleware, name)
		require.Nil(t, got.endpointSettings, name)
		require.False(t, got.eventSinks, name)
	}
}

func TestEventSinkEntriesBecomeConfiguredSinks(t *testing.T) {
	path := contents(t, "flagd.yaml", `
port: 8013
event-sinks:
  - type: webhook
    kinds: [evaluation, configuration_change]
    bufferSize: 500
    url: https://hooks.example.com/flagd
    headers: {Authorization: {env: WEBHOOK_TOKEN}}
  - type: audit
    url: https://audit.example.com
`)
	got, err := loadExtensionSettings(extensible(), path)
	require.NoError(t, err)
	require.True(t, got.eventSinks)
	require.Len(t, got.components.EventSinks, 2)

	first := got.components.EventSinks[0]
	require.Equal(t, "webhook", first.Factory.Type())
	require.Equal(t, map[string]any{
		"kinds":      []any{"evaluation", "configuration_change"},
		"bufferSize": 500, // the case of the key is kept: viper would have made it buffersize
		"url":        "https://hooks.example.com/flagd",
		"headers":    map[string]any{"Authorization": map[string]any{"env": "WEBHOOK_TOKEN"}},
	}, first.Config)
	require.Equal(t, "audit", got.components.EventSinks[1].Factory.Type())
	require.Empty(t, extensible().EventSinks, "the distribution's own components are left as they were")
}

func TestEventSinkEntriesAddToTheMainsOwnSinks(t *testing.T) {
	c := extensible()
	c.EventSinks = []eventsink.Configured{{Factory: sinkFactory{"built-in"}}}
	got, err := loadExtensionSettings(c, contents(t, "f.yaml", "event-sinks:\n  - type: webhook\n"))
	require.NoError(t, err)
	require.Len(t, got.components.EventSinks, 2)
	require.Equal(t, "built-in", got.components.EventSinks[0].Factory.Type())
	require.Len(t, c.EventSinks, 1)
}

func TestMiddlewareEntriesKeepListOrder(t *testing.T) {
	got, err := loadExtensionSettings(extensible(), contents(t, "m.yaml", `
middleware:
  - type: rate-limit
    perSecond: 10
  - type: api-key
    keys: {file: /run/secrets/keys}
`))
	require.NoError(t, err)
	require.Len(t, got.components.Middleware, 2)
	require.Equal(t, "rate-limit", got.components.Middleware[0].Factory.Type())
	require.Equal(t, map[string]any{"perSecond": 10}, got.components.Middleware[0].Config)
	require.Equal(t, "api-key", got.components.Middleware[1].Factory.Type())
	require.Equal(t, map[string]any{"keys": map[string]any{"file": "/run/secrets/keys"}}, got.components.Middleware[1].Config)
	require.False(t, got.eventSinks)
}

func TestEndpointSettings(t *testing.T) {
	got, err := loadExtensionSettings(extensible(), contents(t, "e.yaml", "endpoint-settings:\n  example:\n    greetingPort: 9000\n"))
	require.NoError(t, err)
	require.Equal(t, map[string]map[string]any{"example": {"greetingPort": 9000}}, got.endpointSettings)
}

func TestInvalidExtensionSettingsStopStartup(t *testing.T) {
	prevDistribution, prevHint := Distribution, ComponentsHint
	Distribution, ComponentsHint = "custom", true
	t.Cleanup(func() { Distribution, ComponentsHint = prevDistribution, prevHint })

	for name, tt := range map[string]struct {
		file string
		want []string
	}{
		"unknown sink type": {
			"event-sinks:\n  - type: kafka\n",
			[]string{"event-sinks[0]", `"kafka"`, "custom", "audit, webhook", `"flagd components"`},
		},
		"unknown middleware type": {
			"middleware:\n  - type: api-key\n  - type: jwt\n",
			[]string{"middleware[1]", `"jwt"`, "api-key, rate-limit"},
		},
		"no type":              {"event-sinks:\n  - url: x\n", []string{"event-sinks[0]", `needs a "type"`}},
		"entry not a mapping":  {"event-sinks:\n  - webhook\n", []string{"event-sinks[0]", "expected an entry"}},
		"list not a list":      {"event-sinks:\n  type: webhook\n", []string{"event-sinks", "expected a list"}},
		"unknown endpoint":     {"endpoint-settings:\n  grpc:\n    a: 1\n", []string{"endpoint-settings.grpc", "example, flag-evaluation"}},
		"official endpoint":    {"endpoint-settings:\n  ofrep:\n    port: 1\n", []string{"endpoint-settings.ofrep", "flags of flagd start"}},
		"endpoint not mapping": {"endpoint-settings:\n  example: 3\n", []string{"endpoint-settings.example", "expected the endpoint's settings"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadExtensionSettings(extensible(), contents(t, "bad.yaml", tt.file))
			require.Error(t, err)
			for _, want := range tt.want {
				require.ErrorContains(t, err, want)
			}
		})
	}
}

func TestADistributionWithoutSinksNamesNone(t *testing.T) {
	_, err := loadExtensionSettings(distribution.Components{}, contents(t, "n.yaml", "event-sinks:\n  - type: webhook\n"))
	require.ErrorContains(t, err, "it has: none")
}

func TestAFileThatIsNotYAMLOrJSONCannotCarryExtensionKeys(t *testing.T) {
	path := contents(t, "flagd.toml", "[[event-sinks]]\ntype = \"webhook\"\n")
	_, err := loadExtensionSettings(extensible(), path)
	require.ErrorContains(t, err, "event-sinks")
	require.ErrorContains(t, err, "neither YAML nor JSON")

	_, err = loadExtensionSettings(extensible(), contents(t, "other.toml", "port = 1\n"))
	require.NoError(t, err, "a file without the keys is not this function's business")
}

func TestJSONConfigurationFileIsRead(t *testing.T) {
	got, err := loadExtensionSettings(extensible(),
		contents(t, "flagd.json", `{"event-sinks": [{"type": "webhook", "bufferSize": 7}]}`))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"bufferSize": 7}, got.components.EventSinks[0].Config)
}
