package sync

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSourceConfigJSONRoundTripWithoutHeadersFrom(t *testing.T) {
	config := SourceConfig{
		URI:          "https://flags.example/flags.json",
		Provider:     "http",
		AuthHeader:   "Bearer token",
		Headers:      map[string]string{"X-Team": "payments"},
		Interval:     10,
		StaleAfter:   60,
		Triggers:     []TriggerConfig{{Type: "poller"}},
		TimeoutS:     5,
		Selector:     "flagSetId=a",
		ProviderID:   "p",
		TLS:          true,
		CertPath:     "/etc/ca.pem",
		MaxMsgSize:   1024,
		IntervalSeed: "seed",
	}

	data, err := json.Marshal(config)
	require.NoError(t, err)
	require.NotContains(t, string(data), "headersFrom", "a source without headersFrom serializes as it did")

	var back SourceConfig
	require.NoError(t, json.Unmarshal(data, &back))
	require.Equal(t, config, back)
}
