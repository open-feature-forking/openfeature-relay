package secret

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestResolveFromEnvironment(t *testing.T) {
	t.Setenv("RELAY_TEST_SECRET", "value-from-env")
	got, err := Ref{Env: "RELAY_TEST_SECRET"}.Resolve()
	require.NoError(t, err)
	require.Equal(t, "value-from-env", got)
}

func TestResolveFromFileTrimsSurroundingWhitespace(t *testing.T) {
	got, err := Ref{File: writeFile(t, "  value-from-file\r\n")}.Resolve()
	require.NoError(t, err)
	require.Equal(t, "value-from-file", got)
}

func TestResolveRefusesWhatIsNotThere(t *testing.T) {
	t.Setenv("RELAY_TEST_EMPTY", "")
	tests := []struct {
		name    string
		ref     Ref
		wantErr string
	}{
		{"unset variable", Ref{Env: "RELAY_TEST_UNSET_VARIABLE"}, `environment variable "RELAY_TEST_UNSET_VARIABLE" is not set`},
		{"empty variable", Ref{Env: "RELAY_TEST_EMPTY"}, `environment variable "RELAY_TEST_EMPTY" is empty`},
		{"missing file", Ref{File: filepath.Join(t.TempDir(), "missing")}, "missing"},
		{"empty file", Ref{File: writeFile(t, " \n")}, "is empty"},
		{"neither", Ref{}, "exactly one of env and file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.ref.Resolve()
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestResolveNeverPutsAValueIntoAnError(t *testing.T) {
	const value = "must-not-leak-5d1e"
	t.Setenv("RELAY_TEST_LEAK", value)
	path := writeFile(t, value)

	_, err := Ref{Env: "RELAY_TEST_LEAK", File: path}.Resolve()
	require.Error(t, err, "both env and file is ambiguous")
	require.NotContains(t, err.Error(), value)
}

func TestRefReadsFromJSONAndDescribesWithoutValue(t *testing.T) {
	var ref Ref
	require.NoError(t, json.Unmarshal([]byte(`{"env":"NAME"}`), &ref))
	require.Equal(t, Ref{Env: "NAME"}, ref)
	require.Equal(t, `environment variable "NAME"`, ref.String())

	require.NoError(t, json.Unmarshal([]byte(`{"file":"/run/secrets/key"}`), &ref))
	require.Equal(t, `file "/run/secrets/key"`, Ref{File: "/run/secrets/key"}.String())

	data, err := json.Marshal(Ref{Env: "NAME"})
	require.NoError(t, err)
	require.JSONEq(t, `{"env":"NAME"}`, string(data), "an unset member is not written")
}
