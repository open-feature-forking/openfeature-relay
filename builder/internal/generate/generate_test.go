package generate

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/builder/internal/manifest"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const sample = `dist:
  name: flagd-custom
  version: 1.2.0
  output_path: ./build
  module: example.com/flagd-custom
sync_providers:
  - {module: github.com/open-feature-forking/openfeature-relay/sync/file, version: v0.0.0}
  - {module: github.com/open-feature-forking/openfeature-relay/sync/http, version: v0.0.0}
configuration_formats:
  - {module: github.com/open-feature-forking/openfeature-relay/configformat/flagd, version: v0.0.0}
evaluators:
  - {module: github.com/open-feature-forking/openfeature-relay/evaluator/flagd, version: v0.0.0}
engines:
  - {module: example.com/keyvalue, version: v1.0.0, import: example.com/keyvalue/engine}
service_endpoints:
  - {module: github.com/open-feature-forking/openfeature-relay/service/ofrep, version: v0.0.0}
telemetry: {module: github.com/open-feature-forking/openfeature-relay/telemetry/otel, version: v0.0.0}
middleware:
  - {module: example.com/cors, version: v1.0.0}
event_sinks:
  - {module: example.com/sink, version: v1.0.0}
replaces:
  - {module: github.com/open-feature-forking/openfeature-relay/sync/file, path: ../sync/file}
  - {module: github.com/open-feature-forking/openfeature-relay/core, path: ../core}
`

func parse(t *testing.T, file, text string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse(file, []byte(text))
	require.NoError(t, err)
	return m
}

func TestGoldenFiles(t *testing.T) {
	files, err := Render(parse(t, "/work/dist/manifest.yaml", sample), Options{})
	require.NoError(t, err)

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	require.Equal(t, []string{"components/components.go", "go.mod", "main.go", "probe/main.go"}, names)

	for _, name := range names {
		golden := filepath.Join("testdata", "golden", filepath.FromSlash(name)+".golden")
		if *update {
			require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
			require.NoError(t, os.WriteFile(golden, files[name], 0o644))
		}
		want, err := os.ReadFile(golden)
		require.NoError(t, err)
		require.Equal(t, string(want), string(files[name]), name)
	}
}

func TestReproducibleSource(t *testing.T) {
	first, err := Render(parse(t, "/work/dist/manifest.yaml", sample), Options{})
	require.NoError(t, err)
	second, err := Render(parse(t, "/work/dist/manifest.yaml", sample), Options{})
	require.NoError(t, err)
	require.Equal(t, first, second)

	// the order of the replaces in the manifest does not change the source
	swapped := parse(t, "/work/dist/manifest.yaml", sample)
	swapped.Replaces[0], swapped.Replaces[1] = swapped.Replaces[1], swapped.Replaces[0]
	third, err := Render(swapped, Options{})
	require.NoError(t, err)
	require.Equal(t, first, third)
}

func TestForeignFilesUntouched(t *testing.T) {
	dir := t.TempDir()
	m := parse(t, filepath.Join(dir, "manifest.yaml"), sample)
	out := OutputDir(m)
	foreign := map[string]string{
		"README.md":              "mine",
		"extra.go":               "package main",
		"components/extra.go":    "package components",
		"nested/deeper/file.txt": "keep",
	}
	for name, content := range foreign {
		path := filepath.Join(out, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	written, err := Write(m, Options{})
	require.NoError(t, err)
	require.Equal(t, out, written)

	var present []string
	require.NoError(t, filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(out, path)
			present = append(present, filepath.ToSlash(rel))
		}
		return err
	}))
	sort.Strings(present)
	require.Equal(t, []string{
		"README.md", "components/components.go", "components/extra.go", "extra.go", "go.mod", "main.go",
		"nested/deeper/file.txt", "probe/main.go",
	}, present)
	for name, content := range foreign {
		got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name)))
		require.NoError(t, err)
		require.Equal(t, content, string(got))
	}
}

func TestReplacePathsRelativeToOutput(t *testing.T) {
	files, err := Render(parse(t, "/work/dist/manifest.yaml", sample), Options{})
	require.NoError(t, err)
	// the manifest is in /work/dist and the output in /work/dist/build, so ../core of the manifest is /work/core
	require.Contains(t, string(files["go.mod"]),
		"github.com/open-feature-forking/openfeature-relay/core => ../../core")
	require.Contains(t, string(files["go.mod"]),
		"github.com/open-feature-forking/openfeature-relay/sync/file => ../../sync/file")
}

func TestPinnedRuntimeAndEveryModuleRequiredOnce(t *testing.T) {
	text := sample + "runtime: {module: github.com/open-feature-forking/openfeature-relay/runtime, version: v0.14.0}\n"
	files, err := Render(parse(t, "/work/dist/manifest.yaml", text), Options{CoreVersion: "v0.14.0"})
	require.NoError(t, err)
	gomod := string(files["go.mod"])
	require.Contains(t, gomod, "github.com/open-feature-forking/openfeature-relay/runtime v0.14.0")
	require.Contains(t, gomod, "github.com/open-feature-forking/openfeature-relay/core v0.14.0")
	require.Equal(t, 1, strings.Count(gomod, "example.com/keyvalue v1.0.0"))
}

// TestCustomCmdGolden pins the main package and the go.mod of a distribution that brings its own command line.
func TestCustomCmdGolden(t *testing.T) {
	text := sample + "cmd: {module: example.com/edge, version: v2.0.0, import: example.com/edge/cli}\n"
	files, err := Render(parse(t, "/work/dist/manifest.yaml", text), Options{})
	require.NoError(t, err)

	for _, name := range []string{"main.go", "go.mod"} {
		golden := filepath.Join("testdata", "golden-cmd", name+".golden")
		if *update {
			require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
			require.NoError(t, os.WriteFile(golden, files[name], 0o644))
		}
		want, err := os.ReadFile(golden)
		require.NoError(t, err)
		require.Equal(t, string(want), string(files[name]), name)
	}
	require.Contains(t, string(files["main.go"]), `runtimecmd "example.com/edge/cli"`)
	require.NotContains(t, string(files["go.mod"]), "openfeature-relay/runtime ", "the runtime is not required when the distribution brings its command line")
}
