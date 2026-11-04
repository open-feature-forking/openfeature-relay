package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHeadersFromEnvAndFile(t *testing.T) {
	t.Setenv("RELAY_TEST_ENV_KEY", "key-from-env")
	file := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(file, []byte("token-from-file\n"), 0o600))

	t.Run("resolved values are merged over headers, by canonical name", func(t *testing.T) {
		headers, err := ResolveHeaders(SourceConfig{
			URI:     "https://flags.example/",
			Headers: map[string]string{"x-team": "payments", "x-environment-key": "from-headers"},
			HeadersFrom: map[string]HeaderSource{
				"X-Environment-Key": {Env: "RELAY_TEST_ENV_KEY"},
				"authorization":     {File: file},
			},
		})
		require.NoError(t, err)
		require.Equal(t, map[string]string{
			"X-Team":            "payments",
			"X-Environment-Key": "key-from-env",
			"Authorization":     "token-from-file",
		}, headers)
	})

	t.Run("a source without headersFrom keeps its headers, canonicalized", func(t *testing.T) {
		headers, err := ResolveHeaders(SourceConfig{Headers: map[string]string{"x-team": "payments"}})
		require.NoError(t, err)
		require.Equal(t, map[string]string{"X-Team": "payments"}, headers)
	})

	t.Run("a source without any header has none", func(t *testing.T) {
		headers, err := ResolveHeaders(SourceConfig{URI: "https://flags.example/"})
		require.NoError(t, err)
		require.Empty(t, headers)
	})

	t.Run("headersFrom is read from the sources JSON", func(t *testing.T) {
		var config SourceConfig
		require.NoError(t, json.Unmarshal(
			[]byte(`{"uri":"https://flags.example/","headersFrom":{"X-Environment-Key":{"env":"RELAY_TEST_ENV_KEY"}}}`), &config))
		headers, err := ResolveHeaders(config)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"X-Environment-Key": "key-from-env"}, headers)
	})
}

func TestMissingSecretNamesHeaderNotValue(t *testing.T) {
	const other = "other-secret-8c3d"
	t.Setenv("RELAY_TEST_OTHER", other)

	_, err := ResolveHeaders(SourceConfig{
		URI: "https://flags.example/env-1",
		HeadersFrom: map[string]HeaderSource{
			"X-Other":           {Env: "RELAY_TEST_OTHER"},
			"X-Environment-Key": {Env: "RELAY_TEST_MISSING_KEY"},
		},
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "https://flags.example/env-1", "names the source")
	require.ErrorContains(t, err, "X-Environment-Key", "names the header")
	require.ErrorContains(t, err, "RELAY_TEST_MISSING_KEY", "names the variable")
	require.NotContains(t, err.Error(), other, "never a value")
}

func TestHeadersFromRefusesAnAmbiguousSecret(t *testing.T) {
	_, err := ResolveHeaders(SourceConfig{
		URI:         "https://flags.example/",
		HeadersFrom: map[string]HeaderSource{"X-Key": {Env: "A", File: "/b"}},
	})
	require.ErrorContains(t, err, `header "X-Key"`)
	require.ErrorContains(t, err, "exactly one of env and file")
}
