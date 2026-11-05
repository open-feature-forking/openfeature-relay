package ofrep

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDecodeConfigKeys pins the settings keys the endpoint reads: operators see the list when they mistype one.
func TestDecodeConfigKeys(t *testing.T) {
	_, err := decodeConfig(map[string]any{"nope": 1})
	require.EqualError(t, err, `unknown setting "nope"; the settings are: config, context-values, cors, header-to-context-key-mappings, max-request-body-bytes, max-request-header-bytes, port, service-name, sse-enabled, sse-inactivity-delay, sse-public-url`)
}

// TestDecodeConfigCommonKeys pins where the keys every service endpoint shares land.
func TestDecodeConfigCommonKeys(t *testing.T) {
	cfg, err := decodeConfig(map[string]any{
		"port":                     8016,
		"service-name":             "svc",
		"cors":                     []any{"*"},
		"max-request-header-bytes": 2,
		"max-request-body-bytes":   3,
		"context-values":           map[string]any{"k": "v"},
	})
	require.NoError(t, err)
	require.Equal(t, uint16(8016), cfg.Service.Port)
	require.Equal(t, "svc", cfg.Service.ServiceName)
	require.Equal(t, []string{"*"}, cfg.CORS)
	require.Equal(t, int64(2), cfg.Service.MaxRequestHeaderBytes)
	require.Equal(t, int64(3), cfg.Service.MaxRequestBodyBytes)
	require.Equal(t, map[string]any{"k": "v"}, cfg.ContextValues)

	_, err = decodeConfig(map[string]any{"port": 70000})
	require.EqualError(t, err, `setting "port": 70000 is not a port`)
}
