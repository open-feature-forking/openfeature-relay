package secret

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveSettingsReplacesReferencesAtAnyDepth(t *testing.T) {
	t.Setenv("RELAY_TEST_TOKEN", "token-from-env")
	file := writeFile(t, "key-from-file\n")
	in := map[string]any{
		"url":     "https://hooks.example.com",
		"timeout": "5s",
		"headers": map[string]any{
			"Authorization": map[string]any{"env": "RELAY_TEST_TOKEN"},
			"X-Static":      "literal",
		},
		"keys": []any{map[string]any{"file": file}, "plain"},
	}
	got, err := ResolveSettings(in)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"url":     "https://hooks.example.com",
		"timeout": "5s",
		"headers": map[string]any{"Authorization": "token-from-env", "X-Static": "literal"},
		"keys":    []any{"key-from-file", "plain"},
	}, got)
	// the input is left as it was
	require.Equal(t, map[string]any{"env": "RELAY_TEST_TOKEN"}, in["headers"].(map[string]any)["Authorization"])
}

func TestResolveSettingsLeavesWhatIsNotAReference(t *testing.T) {
	in := map[string]any{
		"a string":           "env",
		"env prefix":         "env:X",
		"shell form":         "${X}",
		"file prefix":        "file:/etc/x",
		"two keys":           map[string]any{"env": "X", "other": "y"},
		"not a string value": map[string]any{"env": 5},
		"empty":              map[string]any{},
		"nil":                nil,
	}
	got, err := ResolveSettings(in)
	require.NoError(t, err)
	require.Equal(t, in, got)

	got, err = ResolveSettings(nil)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestResolveSettingsNamesTheSettingAndTheSourceNeverAValue(t *testing.T) {
	t.Setenv("RELAY_TEST_SET", "super-secret-value")
	_, err := ResolveSettings(map[string]any{
		"headers": map[string]any{
			"Authorization": map[string]any{"env": "RELAY_TEST_SET"},
			"X-Other":       map[string]any{"env": "RELAY_TEST_UNSET_VARIABLE"},
		},
	})
	require.ErrorContains(t, err, `setting "headers.X-Other"`)
	require.ErrorContains(t, err, `"RELAY_TEST_UNSET_VARIABLE"`)
	require.NotContains(t, err.Error(), "super-secret-value")

	_, err = ResolveSettings(map[string]any{"keys": []any{"ok", map[string]any{"file": "/no/such/file"}}})
	require.ErrorContains(t, err, `setting "keys[1]"`)
}
