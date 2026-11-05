package eventcollector

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDecodeConfigKeys pins the settings keys the endpoint reads: operators see the list when they mistype one.
func TestDecodeConfigKeys(t *testing.T) {
	_, err := decodeConfig(map[string]any{"nope": 1})
	require.EqualError(t, err, `unknown setting "nope"; the settings are: cors, max-request-body-bytes, max-request-header-bytes, port, service-name`)
}

// TestDecodeConfigCommonKeys pins where the keys every service endpoint shares land.
func TestDecodeConfigCommonKeys(t *testing.T) {
	cfg, err := decodeConfig(map[string]any{
		"port":                     8016,
		"service-name":             "svc",
		"cors":                     []any{"*"},
		"max-request-header-bytes": 2,
		"max-request-body-bytes":   3,
	})
	require.NoError(t, err)
	require.Equal(t, uint16(8016), cfg.port)
	require.Equal(t, "svc", cfg.serviceName)
	require.Equal(t, []string{"*"}, cfg.cors)
	require.Equal(t, int64(2), cfg.maxRequestHeaderBytes)
	require.Equal(t, int64(3), cfg.maxRequestBodyBytes)

	_, err = decodeConfig(map[string]any{"port": 70000})
	require.EqualError(t, err, `setting "port": 70000 is not a port`)
}
