package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const validManifest = `dist:
  name: flagd-custom
  version: 1.2.0
  output_path: ./build/custom
sync_providers:
  - module: example.com/sync/file
    version: v0.1.0
configuration_formats:
  - module: example.com/configformat/flagd
    version: v0.1.0
service_endpoints:
  - module: example.com/service/ofrep
    version: v0.1.0
`

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func exec(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestCLIValidateOnly: the command validates the manifest and, for now, does nothing else (A1).
func TestCLIValidateOnly(t *testing.T) {
	t.Run("a valid manifest", func(t *testing.T) {
		path := write(t, validManifest)
		code, stdout, stderr := exec("--manifest", path, "--validate-only")
		require.Equal(t, 0, code, stderr)
		require.Contains(t, stdout, "is valid: flagd-custom 1.2.0, 3 component(s)")
		require.Empty(t, stderr)
		entries, err := os.ReadDir(filepath.Dir(path))
		require.NoError(t, err)
		require.Len(t, entries, 1, "nothing is generated")
	})

	t.Run("an invalid manifest names every problem with its position", func(t *testing.T) {
		path := write(t, strings.Replace(strings.Replace(validManifest, "version: v0.1.0", "version: latest", 1), "version: 1.2.0", "version: soon", 1))
		code, stdout, stderr := exec("--manifest", path, "--validate-only")
		require.Equal(t, 1, code)
		require.Empty(t, stdout)
		require.Contains(t, stderr, path+":3:12: dist: version \"soon\"")
		require.Contains(t, stderr, path+":7:14: sync_providers[0]: version \"latest\"")
		require.Contains(t, stderr, "2 problem(s)")
	})

	t.Run("the other flags are accepted", func(t *testing.T) {
		path := write(t, validManifest)
		code, _, stderr := exec("--manifest", path, "--validate-only", "--skip-generate", "--skip-get-modules", "--skip-compile",
			"--skip-strict-versioning", "--go", "/usr/local/go/bin/go", "--verbose")
		require.Equal(t, 0, code, stderr)
	})

	t.Run("the manifest is mandatory", func(t *testing.T) {
		code, stdout, stderr := exec()
		require.Equal(t, 2, code)
		require.Empty(t, stdout)
		require.Contains(t, stderr, "--manifest is mandatory")
	})

	t.Run("an unknown flag or argument", func(t *testing.T) {
		code, _, stderr := exec("--manifest", write(t, validManifest), "--nope")
		require.Equal(t, 2, code)
		require.Contains(t, stderr, "flag provided but not defined: -nope")
		code, _, stderr = exec("--manifest", write(t, validManifest), "extra")
		require.Equal(t, 2, code)
		require.Contains(t, stderr, `unexpected argument "extra"`)
	})

	t.Run("a manifest that cannot be read", func(t *testing.T) {
		code, _, stderr := exec("--manifest", filepath.Join(t.TempDir(), "missing.yaml"))
		require.Equal(t, 3, code)
		require.Contains(t, stderr, "reading the manifest")
	})

	t.Run("help", func(t *testing.T) {
		code, _, stderr := exec("--help")
		require.Equal(t, 0, code)
		require.Contains(t, stderr, "Usage: relay-builder --manifest <file>")
		require.Contains(t, stderr, "-skip-strict-versioning")
	})
}

// TestCLIBuildWithoutSource: with the generate step skipped and no source in the output location, the command
// builds nothing and says so (A4).
func TestCLIBuildWithoutSource(t *testing.T) {
	path := write(t, validManifest)
	code, stdout, stderr := exec("--manifest", path, "--skip-generate")
	require.Equal(t, 4, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "holds no distribution source to build")
}
