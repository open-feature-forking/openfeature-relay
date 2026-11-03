package manifest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/builder/internal/manifest"
	"github.com/stretchr/testify/require"
)

const valid = `dist:
  name: flagd-custom
  version: 1.2.0
  output_path: ./build/custom
sync_providers:
  - module: example.com/sync/file
    version: v0.1.0
configuration_formats:
  - module: example.com/configformat/flagd
    version: v0.1.0
evaluators:
  - module: example.com/evaluator/flagd
    version: v0.1.0
service_endpoints:
  - module: example.com/service/ofrep
    version: v0.1.0
`

func parse(t *testing.T, text string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse("manifest.yaml", []byte(text))
	require.NoError(t, err)
	return m
}

// problems returns "line:column" and the reason of each problem of text, in order.
func problems(t *testing.T, text string) []string {
	t.Helper()
	m, err := manifest.Parse("manifest.yaml", []byte(text))
	if err == nil {
		err = m.Validate()
	}
	if err == nil {
		return nil
	}
	var invalid *manifest.Error
	require.ErrorAs(t, err, &invalid)
	var out []string
	for _, p := range invalid.Problems {
		out = append(out, p.Pos.String()+" "+p.Where+": "+p.Reason)
	}
	return out
}

func TestValidManifest(t *testing.T) {
	m := parse(t, valid)
	require.NoError(t, m.Validate())
	require.Equal(t, "flagd-custom", m.Dist.Name)
	require.Equal(t, "1.2.0", m.Dist.Version)
	require.Equal(t, "./build/custom", m.Dist.OutputPath)
	require.Len(t, m.Entries(), 4)
	require.Equal(t, "flagd", m.BinaryName(), "the binary is named flagd unless the manifest says otherwise")
	require.Equal(t, "flagd-custom", m.ModulePath(), "the module defaults to the distribution's name")

	full := valid + `runtime: {module: example.com/runtime, version: v0.14.0}
engines:
  - module: example.com/engines/vendor
    version: v1.0.0-rc.1
    import: example.com/engines/vendor/engine
telemetry: {module: example.com/telemetry/otel, version: v0.0.0-20260102030405-abcdef123456}
middleware: [{module: example.com/middleware/key, version: v0.3.0}]
event_sinks: [{module: example.com/eventsink/webhook, version: v0.2.0}]
`
	full = strings.Replace(full, "  output_path: ./build/custom\n", "  output_path: ./build/custom\n  module: example.com/flagd-custom\n  binary: relay\n", 1)
	m = parse(t, full)
	require.NoError(t, m.Validate())
	require.Equal(t, "relay", m.BinaryName())
	require.Equal(t, "example.com/flagd-custom", m.ModulePath())
	require.Equal(t, "example.com/runtime", m.Runtime.Module)
	require.Equal(t, "example.com/telemetry/otel", m.Telemetry.Module)
	require.Equal(t, "example.com/engines/vendor/engine", m.Engines[0].Import)
	require.Len(t, m.Entries(), 8, "the runtime is not a component")
}

func TestManifestValidation(t *testing.T) {
	tests := map[string]struct {
		manifest string
		want     []string // line:column, entry, and a part of the reason
	}{
		"no dist": {
			strings.Replace(valid, "dist:\n  name: flagd-custom\n  version: 1.2.0\n  output_path: ./build/custom\n", "", 1),
			[]string{"1:1 : dist is mandatory"},
		},
		"empty dist": {
			strings.Replace(valid, "  name: flagd-custom\n  version: 1.2.0\n  output_path: ./build/custom\n", "  binary: flagd\n", 1),
			[]string{"2:3 dist: name is mandatory", "2:3 dist: version is mandatory", "2:3 dist: output_path is mandatory"},
		},
		"version of the distribution": {
			strings.Replace(valid, "version: 1.2.0", "version: latest", 1),
			[]string{`2:3 dist: version "latest" is not a semantic version`},
		},
		"name of the distribution": {
			strings.Replace(valid, "name: flagd-custom", "name: my distribution", 1),
			[]string{`2:3 dist: name "my distribution"`},
		},
		"binary name": {
			strings.Replace(valid, "output_path: ./build/custom", "output_path: ./build/custom\n  binary: bin/flagd", 1),
			[]string{`2:3 dist: binary "bin/flagd" is not a file name`},
		},
		"unknown key in dist": {
			strings.Replace(valid, "output_path: ./build/custom", "output_path: ./build/custom\n  color: red", 1),
			[]string{`5:3 dist: unknown key "color"`},
		},
		"moving reference": {
			strings.Replace(valid, "version: v0.1.0", "version: latest", 1),
			[]string{`6:5 sync_providers[0]: version "latest" is not an exact semantic version`},
		},
		"version range": {
			strings.Replace(valid, "service_endpoints:\n  - module: example.com/service/ofrep\n    version: v0.1.0", "service_endpoints:\n  - module: example.com/service/ofrep\n    version: ^0.1.0", 1),
			[]string{`15:5 service_endpoints[0]: version "^0.1.0" is not an exact semantic version`},
		},
		"version without v": {
			strings.Replace(valid, "evaluators:\n  - module: example.com/evaluator/flagd\n    version: v0.1.0", "evaluators:\n  - module: example.com/evaluator/flagd\n    version: 0.1.0", 1),
			[]string{`12:5 evaluators[0]: version "0.1.0" is not an exact semantic version`},
		},
		"no version": {
			strings.Replace(valid, "    version: v0.1.0\nconfiguration_formats", "configuration_formats", 1),
			[]string{"6:5 sync_providers[0]: version is mandatory"},
		},
		"no module": {
			strings.Replace(valid, "  - module: example.com/sync/file\n    version", "  - version", 1),
			[]string{"6:5 sync_providers[0]: module is mandatory"},
		},
		"module that is no path": {
			strings.Replace(valid, "example.com/sync/file", "not a path", 1),
			[]string{`6:5 sync_providers[0]: module "not a path" is not a module path`},
		},
		"module listed twice in a group": {
			valid + "middleware:\n  - module: example.com/sync/file\n    version: v0.1.0\n",
			[]string{"18:5 middleware[0]: the module example.com/sync/file is listed more than once (BR-004); it is already at 6:5, sync_providers[0]"},
		},
		"module listed twice across groups": {
			strings.Replace(valid, "  - module: example.com/service/ofrep", "  - module: example.com/evaluator/flagd", 1),
			[]string{"15:5 service_endpoints[0]: the module example.com/evaluator/flagd is listed more than once (BR-004); it is already at 12:5, evaluators[0]"},
		},
		"the runtime is a module too": {
			valid + "runtime: {module: example.com/sync/file, version: v0.1.0}\n",
			[]string{"6:5 sync_providers[0]: the module example.com/sync/file is listed more than once (BR-004); it is already at 17:10, runtime"},
		},
		"import outside the module": {
			strings.Replace(valid, "  - module: example.com/sync/file\n    version: v0.1.0", "  - module: example.com/sync/file\n    version: v0.1.0\n    import: example.com/other/pkg", 1),
			[]string{`6:5 sync_providers[0]: import "example.com/other/pkg" is not a package of the module example.com/sync/file`},
		},
		"no sync provider": {
			strings.Replace(valid, "sync_providers:\n  - module: example.com/sync/file\n    version: v0.1.0\n", "sync_providers: []\n", 1),
			[]string{"5:1 : no sync provider is selected (BR-002"},
		},
		"no configuration format": {
			strings.Replace(valid, "configuration_formats:\n  - module: example.com/configformat/flagd\n    version: v0.1.0\n", "", 1),
			[]string{"1:1 : no configuration format is selected (BR-002"},
		},
		"no service endpoint": {
			strings.Replace(valid, "service_endpoints:\n  - module: example.com/service/ofrep\n    version: v0.1.0\n", "service_endpoints: []\n", 1),
			[]string{"14:1 : no service endpoint is selected (BR-002"},
		},
		"an engine is a configuration format": {
			strings.Replace(valid, "configuration_formats:\n  - module: example.com/configformat/flagd\n    version: v0.1.0\nevaluators:\n  - module: example.com/evaluator/flagd\n    version: v0.1.0\n", "engines:\n  - module: example.com/engine/vendor\n    version: v1.0.0\n", 1),
			nil,
		},
		"nothing listed": {
			"dist:\n  name: x\n  version: 1.0.0\n  output_path: out\n",
			[]string{
				"1:1 : no component is listed (BR-001", "1:1 : no sync provider is selected", "1:1 : no configuration format is selected",
				"1:1 : no service endpoint is selected",
			},
		},
		"telemetry list": {
			valid + "telemetry:\n  - module: example.com/telemetry/a\n    version: v0.1.0\n  - module: example.com/telemetry/b\n    version: v0.1.0\n",
			[]string{"18:3 telemetry: at most one telemetry provider is selected"},
		},
		"unknown group": {
			valid + "sources:\n  - module: example.com/x\n    version: v0.1.0\n",
			[]string{`17:1 : unknown key "sources" (allowed: dist, runtime,`},
		},
		"unknown key in an entry": {
			strings.Replace(valid, "    version: v0.1.0\nconfiguration_formats", "    version: v0.1.0\n    local: ../x\nconfiguration_formats", 1),
			[]string{`8:5 sync_providers[0]: unknown key "local" (allowed: module, version, import)`},
		},
		"group is not a list": {
			strings.Replace(valid, "sync_providers:\n  - module: example.com/sync/file\n    version: v0.1.0\n", "sync_providers: example.com/sync/file\n", 1),
			[]string{"5:17 sync_providers: must be a list"},
		},
		"entry is not a mapping": {
			strings.Replace(valid, "  - module: example.com/sync/file\n    version: v0.1.0", "  - example.com/sync/file@v0.1.0", 1),
			[]string{"6:5 sync_providers[0]: must be a mapping"},
		},
		"key twice": {
			valid + "dist:\n  name: again\n",
			[]string{`17:1 : the key "dist" appears more than once`},
		},
		"empty manifest": {
			"",
			[]string{"1:1 : the manifest is empty"},
		},
		"not a mapping": {
			"- a\n- b\n",
			[]string{"1:1 : the manifest must be a mapping"},
		},
		"not YAML": {
			"dist: [unclosed\n",
			[]string{"1:1 : not valid YAML"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := problems(t, tt.manifest)
			if tt.want == nil {
				require.Empty(t, got)
				return
			}
			require.Len(t, got, len(tt.want), "problems: %v", got)
			for i, want := range tt.want {
				require.Contains(t, got[i], want)
			}
		})
	}
}

// One run names every faulty entry (A1), each at its own position, in manifest order.
func TestEveryProblemIsReportedWithItsPosition(t *testing.T) {
	text := `dist:
  name: flagd-custom
  version: soon
  output_path: out
sync_providers:
  - module: example.com/sync/file
    version: latest
  - module: example.com/sync/http
    version: v1
configuration_formats:
  - module: example.com/sync/file
    version: v0.1.0
service_endpoints:
  - version: v0.1.0
`
	got := problems(t, text)
	require.Len(t, got, 5, "problems: %v", got)
	require.Contains(t, got[0], "2:3 dist: version \"soon\"")
	require.Contains(t, got[1], "6:5 sync_providers[0]: version \"latest\"")
	require.Contains(t, got[2], "8:5 sync_providers[1]: version \"v1\"")
	require.Contains(t, got[3], "11:5 configuration_formats[0]: the module example.com/sync/file is listed more than once")
	require.Contains(t, got[4], "14:5 service_endpoints[0]: module is mandatory")

	m := parse(t, text)
	err := m.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "manifest.yaml:6:5: sync_providers[0]:")
	require.Contains(t, err.Error(), "5 problem(s)")
}

func TestLoadReportsAnUnreadableFileAndAnInvalidOne(t *testing.T) {
	_, err := manifest.Load(filepath.Join(t.TempDir(), "missing.yaml"))
	require.ErrorContains(t, err, "reading the manifest")
	var invalid *manifest.Error
	require.NotErrorAs(t, err, &invalid)

	path := filepath.Join(t.TempDir(), "manifest.yaml")
	require.NoError(t, os.WriteFile(path, []byte("dist: {}\n"), 0o600))
	_, err = manifest.Load(path)
	require.ErrorAs(t, err, &invalid)
	require.Equal(t, path, invalid.Problems[0].File)

	require.NoError(t, os.WriteFile(path, []byte(valid), 0o600))
	m, err := manifest.Load(path)
	require.NoError(t, err)
	require.Equal(t, path, m.File)
}

// writeModule creates a directory holding a go.mod for the module.
func writeModule(t *testing.T, dir, module string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("// the module\nmodule "+module+" // comment\n\ngo 1.27\n"), 0o600))
}

// BR-007: a replace maps a module to a directory that contains that module, read relative to the manifest.
func TestReplaceToMissingOrWrongModule(t *testing.T) {
	root := t.TempDir()
	writeModule(t, filepath.Join(root, "components", "sync", "file"), "example.com/sync/file")
	writeModule(t, filepath.Join(root, "components", "other"), "example.com/other")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "components", "nomod"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "components", "afile"), []byte("x"), 0o600))
	dir := filepath.Join(root, "distributions", "custom")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	check := func(replaces string) ([]string, error) {
		path := filepath.Join(dir, "manifest.yaml")
		require.NoError(t, os.WriteFile(path, []byte(valid+"replaces:\n"+replaces), 0o600))
		m, err := manifest.Parse(path, []byte(valid+"replaces:\n"+replaces))
		require.NoError(t, err)
		err = m.Validate()
		if err == nil {
			return nil, nil
		}
		var invalid *manifest.Error
		require.ErrorAs(t, err, &invalid)
		var out []string
		for _, p := range invalid.Problems {
			out = append(out, p.Pos.String()+" "+p.Where+": "+p.Reason)
		}
		return out, err
	}

	t.Run("a directory that holds the module", func(t *testing.T) {
		got, _ := check("  - {module: example.com/sync/file, path: ../../components/sync/file}\n")
		require.Empty(t, got)
	})
	t.Run("an absolute path", func(t *testing.T) {
		got, _ := check("  - {module: example.com/sync/file, path: " + filepath.ToSlash(filepath.Join(root, "components", "sync", "file")) + "}\n")
		require.Empty(t, got)
	})
	t.Run("the path is read relative to the manifest, not to the working directory", func(t *testing.T) {
		got, _ := check("  - {module: example.com/sync/file, path: components/sync/file}\n")
		require.Len(t, got, 1)
		require.Contains(t, got[0], "18:5 replaces[0]")
		require.Contains(t, got[0], "does not exist")
		require.Contains(t, got[0], "(BR-007)")
	})
	t.Run("a missing directory", func(t *testing.T) {
		got, _ := check("  - {module: example.com/sync/file, path: ../../nowhere}\n")
		require.Len(t, got, 1)
		require.Contains(t, got[0], `the path "../../nowhere" for example.com/sync/file does not exist`)
	})
	t.Run("a file", func(t *testing.T) {
		got, _ := check("  - {module: example.com/sync/file, path: ../../components/afile}\n")
		require.Len(t, got, 1)
		require.Contains(t, got[0], "is not a directory")
	})
	t.Run("a directory without go.mod", func(t *testing.T) {
		got, _ := check("  - {module: example.com/sync/file, path: ../../components/nomod}\n")
		require.Len(t, got, 1)
		require.Contains(t, got[0], "holds no go.mod")
	})
	t.Run("a directory that holds another module", func(t *testing.T) {
		got, _ := check("  - {module: example.com/sync/file, path: ../../components/other}\n")
		require.Len(t, got, 1)
		require.Contains(t, got[0], "holds the module example.com/other, not example.com/sync/file")
	})
	t.Run("every faulty replace is reported", func(t *testing.T) {
		got, _ := check("  - {module: example.com/sync/file, path: ../../nowhere}\n  - {module: example.com/other, path: ../../components/sync/file}\n  - {module: example.com/sync/file, path: ../../components/sync/file}\n  - {path: x}\n  - {module: example.com/x}\n")
		require.Len(t, got, 5, "problems: %v", got)
		require.Contains(t, got[0], "18:5 replaces[0]")
		require.Contains(t, got[1], "19:5 replaces[1]")
		require.Contains(t, got[2], "20:5 replaces[2]: the module example.com/sync/file is replaced more than once")
		require.Contains(t, got[3], "21:5 replaces[3]: module is mandatory")
		require.Contains(t, got[4], "22:5 replaces[4]: path is mandatory")
	})
	t.Run("an unknown key", func(t *testing.T) {
		got, _ := check("  - {module: example.com/sync/file, path: ../../components/sync/file, local: yes}\n")
		require.Len(t, got, 1)
		require.Contains(t, got[0], `unknown key "local" (allowed: module, path)`)
	})

	m := parse(t, valid+"replaces:\n  - {module: example.com/sync/file, path: ../x}\n")
	m.File = filepath.Join(dir, "manifest.yaml")
	require.Equal(t, filepath.Join(root, "distributions", "x"), m.ReplacePath(m.Replaces[0]))
}
