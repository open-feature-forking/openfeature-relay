package generate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutputDir(t *testing.T) {
	m := parse(t, "/work/dist/manifest.yaml", sample)
	require.Equal(t, filepath.FromSlash("/work/dist/build"), OutputDir(m), "relative to the manifest")

	abs, err := filepath.Abs("/elsewhere/./out/")
	require.NoError(t, err)
	m.Dist.OutputPath = abs
	require.Equal(t, filepath.Clean(abs), OutputDir(m), "an absolute path is taken as it is, cleaned")
}

// TestWriteProbe pins that the probe is written as RenderProbe renders it, into a directory of its own.
func TestWriteProbe(t *testing.T) {
	m := parse(t, "/work/dist/manifest.yaml", sample)
	probeDir := filepath.Join(t.TempDir(), "probe")
	require.NoError(t, WriteProbe(m, Options{}, "/work/dist/build", probeDir))

	want, err := RenderProbe(m, Options{}, "/work/dist/build")
	require.NoError(t, err)
	for name, content := range want {
		got, err := os.ReadFile(filepath.Join(probeDir, filepath.FromSlash(name)))
		require.NoError(t, err)
		require.Equal(t, string(content), string(got), name)
	}
	entries, err := os.ReadDir(probeDir)
	require.NoError(t, err)
	require.Len(t, entries, len(want), "nothing but the probe")
}

func TestWriteProbeUnwritable(t *testing.T) {
	m := parse(t, "/work/dist/manifest.yaml", sample)
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	require.Error(t, WriteProbe(m, Options{}, "/work/dist/build", filepath.Join(file, "probe")))
}
