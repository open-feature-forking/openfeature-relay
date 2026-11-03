package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEndpointDecodesConfig(t *testing.T) {
	cfg, err := decodeConfig(map[string]any{
		"port": 8013, "management-port": uint16(8014), "service-name": "flagd", "cert-path": "c.pem", "key-path": "k.pem",
		"socket-path": "/tmp/s", "cors": []string{"*"}, "context-values": map[string]any{"a": "b"},
		"header-to-context-key-mappings": map[string]string{"X-A": "a"}, "stream-deadline": "7s",
		"max-request-header-bytes": 1024, "max-request-body-bytes": int64(2048),
	})

	require.NoError(t, err)
	c := cfg.Configuration
	require.Equal(t, uint16(8013), c.Port)
	require.Equal(t, uint16(8014), c.ManagementPort)
	require.Equal(t, "flagd", c.ServiceName)
	require.Equal(t, "c.pem", c.CertPath)
	require.Equal(t, "k.pem", c.KeyPath)
	require.Equal(t, "/tmp/s", c.SocketPath)
	require.Equal(t, []string{"*"}, c.CORS)
	require.Equal(t, map[string]any{"a": "b"}, c.ContextValues)
	require.Equal(t, map[string]string{"X-A": "a"}, c.HeaderToContextKeyMappings)
	require.Equal(t, 7*time.Second, c.StreamDeadline)
	require.Equal(t, int64(1024), c.MaxRequestHeaderBytes)
	require.Equal(t, int64(2048), c.MaxRequestBodyBytes)

	_, err = decodeConfig(map[string]any{"prot": 1})
	require.ErrorContains(t, err, `unknown setting "prot"`)
	_, err = decodeConfig(map[string]any{"port": "high"})
	require.ErrorContains(t, err, `setting "port"`)
}
