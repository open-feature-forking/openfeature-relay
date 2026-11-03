package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfigDecoder(t *testing.T) {
	var (
		port uint16
		on   bool
		wait time.Duration
		cors []string
	)
	d := ConfigDecoder{
		"port": Set(&port, Uint16), "enabled": Set(&on, Bool), "wait": Set(&wait, Duration), "cors": Set(&cors, Strings),
	}

	require.NoError(t, d.Decode(map[string]any{"port": 8013, "enabled": true, "wait": "5s", "cors": []any{"*"}}))
	require.Equal(t, uint16(8013), port)
	require.True(t, on)
	require.Equal(t, 5*time.Second, wait)
	require.Equal(t, []string{"*"}, cors)

	require.NoError(t, d.Decode(map[string]any{"port": float64(9), "wait": 2 * time.Second}))
	require.Equal(t, uint16(9), port)
	require.Equal(t, 2*time.Second, wait)

	require.EqualError(t, d.Decode(map[string]any{"prot": 1}), `unknown setting "prot"; the settings are: cors, enabled, port, wait`)
	require.EqualError(t, d.Decode(map[string]any{"port": 70000}), `setting "port": 70000 is not a port`)
	require.EqualError(t, d.Decode(map[string]any{"enabled": "yes"}), `setting "enabled": want true or false, got yes (string)`)
	require.NoError(t, d.Decode(map[string]any{"config": 1}, "config"), "an accepted key is left to the caller")
}

func TestConfigConversions(t *testing.T) {
	_, err := Int64(1.5)
	require.Error(t, err)
	m, err := StringMap(map[string]any{"a": "b"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"a": "b"}, m)
	_, err = StringMap(map[string]any{"a": 1})
	require.Error(t, err)
	_, err = Duration("soon")
	require.Error(t, err)
}
