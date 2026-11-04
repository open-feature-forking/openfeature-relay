package cmd

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/stretchr/testify/require"
)

func withCollector() distribution.Components {
	c := extensible()
	c.Endpoints = append(c.Endpoints, typedEndpoint{"event-collector"})
	return c
}

// TestEventCollectorKeyIsTheCollectorsEndpointSettings pins that the event-collector key of the configuration file
// carries the settings of the event collector endpoint, port included, and that endpoint-settings wins over it.
func TestEventCollectorKeyIsTheCollectorsEndpointSettings(t *testing.T) {
	got, err := loadExtensionSettings(withCollector(), contents(t, "c.yaml", "event-collector:\n  port: 8017\n  cors: ['https://app.example.com']\n"))
	require.NoError(t, err)
	require.Equal(t, map[string]map[string]any{"event-collector": {"port": 8017, "cors": []any{"https://app.example.com"}}}, got.endpointSettings)
	require.False(t, got.eventSinks)

	got, err = loadExtensionSettings(withCollector(), contents(t, "w.yaml", `
event-collector:
  port: 8017
  max-request-body-bytes: 100
endpoint-settings:
  event-collector:
    max-request-body-bytes: 200
  example:
    greetingPort: 9000
`))
	require.NoError(t, err)
	require.Equal(t, map[string]map[string]any{
		"event-collector": {"port": 8017, "max-request-body-bytes": 200},
		"example":         {"greetingPort": 9000},
	}, got.endpointSettings, "endpoint-settings wins where both set the same key, and each adds what the other lacks")
}

func TestEventCollectorKeyWithoutSettingsChangesNothing(t *testing.T) {
	for name, file := range map[string]string{"empty mapping": "event-collector: {}\n", "null": "event-collector:\n", "absent": "port: 8013\n"} {
		got, err := loadExtensionSettings(withCollector(), contents(t, "e.yaml", file))
		require.NoError(t, err, name)
		require.Nil(t, got.endpointSettings, name)
	}
}

func TestEventCollectorKeyInvalid(t *testing.T) {
	prevDistribution, prevHint := Distribution, ComponentsHint
	Distribution, ComponentsHint = "minimal", false
	t.Cleanup(func() { Distribution, ComponentsHint = prevDistribution, prevHint })

	_, err := loadExtensionSettings(withCollector(), contents(t, "l.yaml", "event-collector: [8017]\n"))
	require.ErrorContains(t, err, "event-collector: expected the endpoint's settings")

	// a distribution without the collector refuses settings for it instead of ignoring them
	_, err = loadExtensionSettings(distribution.Components{Endpoints: []service.Factory{typedEndpoint{"ofrep"}}},
		contents(t, "a.yaml", "event-collector:\n  port: 8017\n"))
	require.ErrorContains(t, err, "event-collector")
	require.ErrorContains(t, err, "minimal has no service endpoint of this type")
	require.ErrorContains(t, err, "it has: ofrep")
}
