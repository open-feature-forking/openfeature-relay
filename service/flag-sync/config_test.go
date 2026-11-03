package sync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEndpointDecodesConfig(t *testing.T) {
	cfg, err := decodeConfig(map[string]any{
		"port": 8015, "cert-path": "c.pem", "key-path": "k.pem", "socket-path": "/tmp/s", "service-name": "flagd",
		"http-enabled": true, "disable-sync-metadata": true, "cors": []string{"*"}, "context-values": map[string]any{"a": "b"},
		"stream-deadline": 3 * time.Second, "max-request-header-bytes": 10, "max-request-body-bytes": 20,
	})

	require.NoError(t, err)
	require.Equal(t, uint16(8015), cfg.Port)
	require.Equal(t, "c.pem", cfg.CertPath)
	require.Equal(t, "k.pem", cfg.KeyPath)
	require.Equal(t, "/tmp/s", cfg.SocketPath)
	require.Equal(t, "flagd", cfg.ServiceName)
	require.True(t, cfg.HTTPEnabled)
	require.True(t, cfg.DisableSyncMetadata)
	require.Equal(t, []string{"*"}, cfg.CORS)
	require.Equal(t, map[string]any{"a": "b"}, cfg.ContextValues)
	require.Equal(t, 3*time.Second, cfg.StreamDeadline)
	require.Equal(t, int64(10), cfg.MaxRequestHeaderBytes)
	require.Equal(t, int64(20), cfg.MaxRequestBodyBytes)

	_, err = decodeConfig(map[string]any{"sources": []string{"a"}})
	require.ErrorContains(t, err, `unknown setting "sources"`)
}
