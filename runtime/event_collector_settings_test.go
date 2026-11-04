package runtime

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEventCollectorGetsFlagdsFlagsLikeOFREP pins that --cors-origin, --max-request-body and --max-request-header
// reach the event collector with the values OFREP gets, so that a browser an operator allowed to reach OFREP can reach
// the collector and a limit on request bodies limits both. The collector's port is not a flag: without one set by the
// operator it opens none.
func TestEventCollectorGetsFlagsLikeOFREP(t *testing.T) {
	config := Config{CORS: []string{"https://app.example.com"}, MaxRequestBodyBytes: 5000, MaxRequestHeaderBytes: 6000, OfrepServicePort: 8016}

	got := endpointSettings(config, eventCollectorType)
	require.Equal(t, map[string]any{
		"service-name":             "flagd",
		"cors":                     []string{"https://app.example.com"},
		"max-request-body-bytes":   int64(5000),
		"max-request-header-bytes": int64(6000),
	}, got)
	require.NotContains(t, got, "port", "the collector opens no port unless the operator sets one")

	ofrep := endpointSettings(config, ofrepType)
	for _, key := range []string{"service-name", "cors", "max-request-body-bytes", "max-request-header-bytes"} {
		require.Equal(t, ofrep[key], got[key], key)
	}
}

// TestEndpointSettingsOfTheOperatorWinOverTheFlagsForTheCollector pins the precedence: what the operator writes for the
// collector in the configuration file wins over the flags, and adds the port.
func TestEndpointSettingsOfTheOperatorWinOverTheFlagsForTheCollector(t *testing.T) {
	config := Config{
		CORS: []string{"https://app.example.com"}, MaxRequestBodyBytes: 5000,
		EndpointSettings: map[string]map[string]any{eventCollectorType: {"port": 8017, "cors": []string{"*"}, "max-request-body-bytes": int64(10)}},
	}

	got := endpointSettings(config, eventCollectorType)
	require.Equal(t, 8017, got["port"])
	require.Equal(t, []string{"*"}, got["cors"])
	require.Equal(t, int64(10), got["max-request-body-bytes"])
	require.Equal(t, "flagd", got["service-name"])
	require.Equal(t, config.MaxRequestHeaderBytes, got["max-request-header-bytes"], "a flag the operator left alone still applies")
}
