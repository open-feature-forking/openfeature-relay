package ofrep

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEndpointDecodesConfig(t *testing.T) {
	cfg, err := decodeConfig(map[string]any{
		"port": 8016, "service-name": "flagd", "sse-public-url": "https://x", "sse-enabled": true, "sse-inactivity-delay": 30,
		"cors": []string{"*"}, "context-values": map[string]any{"a": "b"},
		"header-to-context-key-mappings": map[string]string{"X-A": "a"}, "max-request-header-bytes": 10,
		"max-request-body-bytes": 20,
	})

	require.NoError(t, err)
	require.Equal(t, uint16(8016), cfg.Service.Port)
	require.Equal(t, "flagd", cfg.Service.ServiceName)
	require.Equal(t, "https://x", cfg.Service.SSEPublicURL)
	require.True(t, cfg.Service.SSEEnabled)
	require.Equal(t, 30, cfg.Service.SSEInactivityDelaySec)
	require.Equal(t, []string{"*"}, cfg.CORS)
	require.Equal(t, map[string]any{"a": "b"}, cfg.ContextValues)
	require.Equal(t, map[string]string{"X-A": "a"}, cfg.HeaderToContextKeyMappings)
	require.Equal(t, int64(10), cfg.Service.MaxRequestHeaderBytes)
	require.Equal(t, int64(20), cfg.Service.MaxRequestBodyBytes)

	_, err = decodeConfig(map[string]any{"formats": []string{"x"}})
	require.ErrorContains(t, err, `unknown setting "formats"`)
}
