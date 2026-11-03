package build

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/builder/internal/generate"
	"github.com/open-feature-forking/openfeature-relay/builder/internal/gotool"
	"github.com/open-feature-forking/openfeature-relay/builder/internal/manifest"
	"github.com/open-feature-forking/openfeature-relay/builder/internal/needs"
	"github.com/stretchr/testify/require"
)

const (
	core = "github.com/open-feature-forking/openfeature-relay/core"
	rt   = "github.com/open-feature-forking/openfeature-relay/runtime"
)

// stubs are the modules a distribution of these tests builds from: the parts of core and the runtime the generated
// source uses, and one component per group. Every module is replaced by its directory, so no test needs the network.
var stubs = map[string]map[string]string{
	"core": {
		"go.mod": "module " + core + "\n\ngo 1.26.0\n",
		"pkg/distribution/distribution.go": `package distribution

import (
	"` + core + `/pkg/configformat"
	"` + core + `/pkg/evaluator"
	"` + core + `/pkg/service"
	"` + core + `/pkg/sync"
)

type Components struct {
	Sync                 []sync.Factory
	ConfigurationFormats []configformat.Factory
	Evaluators           []evaluator.Factory
	Endpoints            []service.Factory
}

type Info struct{ Name, Version, Commit, Date string }

type Group string

const (
	GroupSync                Group = "sync_providers"
	GroupConfigurationFormat Group = "configuration_formats"
	GroupEvaluator           Group = "evaluators"
	GroupServiceEndpoint     Group = "service_endpoints"
)

type ComponentInfo struct {
	Group  Group
	Type   string
	Module string
}

type Inventory struct{ Components []ComponentInfo }
`,
		"pkg/sync/sync.go": `package sync

type Factory interface{ Type() string }

type SchemeFactory interface {
	Factory
	Schemes() []string
}
`,
		"pkg/configformat/configformat.go": "package configformat\n\ntype Factory interface{ Type() string }\n",
		"pkg/evaluator/evaluator.go":       "package evaluator\n\ntype Factory interface {\n\tType() string\n\tConfigurationFormat() string\n}\n",
		"pkg/service/service.go": `package service

type Needs struct {
	Router bool
	Format string
}

type Factory interface {
	Type() string
	Needs() Needs
}
`,
	},
	"runtime": {
		"go.mod": "module " + rt + "\n\ngo 1.26.0\n\nrequire " + core + " v0.0.0\n",
		"cmd/cmd.go": `package cmd

import (
	"fmt"

	"` + core + `/pkg/distribution"
)

func Execute(info distribution.Info, c distribution.Components, inv distribution.Inventory) {
	fmt.Println(info.Name, info.Version, info.Commit, info.Date, len(inv.Components))
}
`,
	},
	"file": component("file", "sync", "NewFactories() []sync.Factory", "[]sync.Factory{factory{}}",
		"type factory struct{}\n\nfunc (factory) Type() string { return \"file\" }\nfunc (factory) Schemes() []string { return []string{\"file:\"} }\n"),
	"flagdformat": component("flagdformat", "configformat", "NewFactory() configformat.Factory", "factory{}",
		"type factory struct{}\n\nfunc (factory) Type() string { return \"flagd\" }\n"),
	"flagdeval": component("flagdeval", "evaluator", "NewFactory() evaluator.Factory", "factory{}",
		"type factory struct{}\n\nfunc (factory) Type() string { return \"flagd\" }\nfunc (factory) ConfigurationFormat() string { return \"flagd\" }\n"),
	"ofrep": component("ofrep", "service", "NewFactory() service.Factory", "factory{}",
		"type factory struct{}\n\nfunc (factory) Type() string { return \"ofrep\" }\nfunc (factory) Needs() service.Needs { return service.Needs{Router: true} }\n"),
	"flagsync": component("flagsync", "service", "NewFactory() service.Factory", "factory{}",
		"type factory struct{}\n\nfunc (factory) Type() string { return \"flag-sync\" }\nfunc (factory) Needs() service.Needs { return service.Needs{} }\n"),
}

func component(name, corePkg, signature, value, types string) map[string]string {
	return map[string]string{
		"go.mod": "module example.test/" + name + "\n\ngo 1.26.0\n\nrequire " + core + " v0.0.0\n",
		name + ".go": "package " + name + "\n\nimport \"" + core + "/pkg/" + corePkg + "\"\n\n" + types +
			"\nfunc " + signature + " { return " + value + " }\n",
	}
}

type fixture struct {
	dir      string
	manifest string
}

// setup writes the stub modules and a manifest that selects the named components.
func setup(t *testing.T, selected ...string) *fixture {
	t.Helper()
	dir := t.TempDir()
	for name, files := range stubs {
		for file, content := range files {
			path := filepath.Join(dir, "stubs", name, filepath.FromSlash(file))
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		}
	}
	group := map[string]string{
		"file": "sync_providers", "flagdformat": "configuration_formats", "flagdeval": "evaluators",
		"ofrep": "service_endpoints", "flagsync": "service_endpoints",
	}
	var text strings.Builder
	text.WriteString("dist: {name: custom, version: 1.2.3, output_path: ./out, module: example.test/custom, binary: relay}\n")
	for _, g := range []string{"sync_providers", "configuration_formats", "evaluators", "service_endpoints"} {
		text.WriteString(g + ":\n")
		for _, name := range selected {
			if group[name] == g {
				text.WriteString("  - {module: example.test/" + name + ", version: v0.0.0}\n")
			}
		}
	}
	text.WriteString("replaces:\n  - {module: " + core + ", path: ./stubs/core}\n  - {module: " + rt + ", path: ./stubs/runtime}\n")
	for _, name := range selected {
		text.WriteString("  - {module: example.test/" + name + ", path: ./stubs/" + name + "}\n")
	}
	f := &fixture{dir: dir, manifest: filepath.Join(dir, "manifest.yaml")}
	require.NoError(t, os.WriteFile(f.manifest, []byte(text.String()), 0o644))
	return f
}

func (f *fixture) load(t *testing.T) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Load(f.manifest)
	require.NoError(t, err)
	return m
}

// options runs the go binary that runs the tests, offline and with its own module cache.
func options(t *testing.T) (Options, *bytes.Buffer) {
	t.Helper()
	log := &bytes.Buffer{}
	env := append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOFLAGS=-modcacherw", "GOTOOLCHAIN=local", "GOMODCACHE="+t.TempDir())
	return Options{
		Tool: gotool.Tool{Go: filepath.Join(runtime.GOROOT(), "bin", "go"), Env: env, Log: log},
		Date: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}, log
}

var ctx = context.Background()

func TestFullBuildStampsTheBinary(t *testing.T) {
	f := setup(t, "file", "flagdformat", "flagdeval", "ofrep")
	opts, _ := options(t)

	res, err := Run(ctx, f.load(t), opts)

	require.NoError(t, err)
	require.Equal(t, filepath.Join(f.dir, "out", "relay"), res.Binary)
	out, err := exec.Command(res.Binary).Output()
	require.NoError(t, err)
	require.Equal(t, "custom 1.2.3 unknown 2026-01-02T03:04:05Z 4\n", string(out), "version, revision and date are stamped; the revision is unknown outside a repository")
	require.NoFileExists(t, filepath.Join(f.dir, "out", ".relay.tmp"))
}

func TestSkipGenerateReusesTheSource(t *testing.T) {
	f := setup(t, "file", "flagdformat", "flagdeval", "ofrep")
	opts, _ := options(t)
	_, err := Run(ctx, f.load(t), opts)
	require.NoError(t, err)
	mainGo := filepath.Join(f.dir, "out", "main.go")
	source, err := os.ReadFile(mainGo)
	require.NoError(t, err)
	edited := append(source, []byte("\n// edited by hand\n")...)
	require.NoError(t, os.WriteFile(mainGo, edited, 0o644))

	opts.SkipGenerate = true
	res, err := Run(ctx, f.load(t), opts)

	require.NoError(t, err)
	require.NotEmpty(t, res.Binary)
	after, err := os.ReadFile(mainGo)
	require.NoError(t, err)
	require.Equal(t, string(edited), string(after), "the source stays as it is")
}

func TestSkipGenerateWithoutSource(t *testing.T) {
	f := setup(t, "file", "flagdformat", "flagdeval", "ofrep")
	opts, log := options(t)
	opts.SkipGenerate = true

	res, err := Run(ctx, f.load(t), opts)

	var none *NoSourceError
	require.ErrorAs(t, err, &none)
	require.Equal(t, filepath.Join(f.dir, "out"), none.Dir)
	require.Empty(t, res.Binary)
	require.Empty(t, log.String(), "the Go Toolchain is not run")
	require.NoDirExists(t, filepath.Join(f.dir, "out"), "nothing is generated")
}

func TestSkipGetModulesUsesTheRecordedDependencies(t *testing.T) {
	f := setup(t, "file", "flagdformat", "flagdeval", "ofrep")
	opts, log := options(t)
	_, err := Run(ctx, f.load(t), opts)
	require.NoError(t, err)
	log.Reset()

	opts.SkipGenerate, opts.SkipGetModules = true, true
	res, err := Run(ctx, f.load(t), opts)

	require.NoError(t, err)
	require.NotEmpty(t, res.Binary)
	require.NotContains(t, log.String(), "mod tidy")
	require.NotContains(t, log.String(), "list -m")
}

func TestSkipCompileLeavesTheSource(t *testing.T) {
	f := setup(t, "file", "flagdformat", "flagdeval", "ofrep")
	opts, log := options(t)
	opts.SkipCompile = true

	res, err := Run(ctx, f.load(t), opts)

	require.NoError(t, err)
	require.Empty(t, res.Binary)
	require.Equal(t, filepath.Join(f.dir, "out"), res.SourceDir)
	require.FileExists(t, filepath.Join(f.dir, "out", "main.go"))
	require.NoFileExists(t, filepath.Join(f.dir, "out", "relay"))
	require.NotContains(t, log.String(), "go build")
}

func TestStaleBinaryNotReported(t *testing.T) {
	f := setup(t, "file", "flagdformat", "flagdeval", "ofrep")
	opts, _ := options(t)
	_, err := Run(ctx, f.load(t), opts)
	require.NoError(t, err)
	binary := filepath.Join(f.dir, "out", "relay")
	before, err := os.ReadFile(binary)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "out", "main.go"), []byte("package main\n\nfunc main() { undefined() }\n"), 0o644))

	opts.SkipGenerate = true
	res, err := Run(ctx, f.load(t), opts)

	var cerr *gotool.CompileError
	require.ErrorAs(t, err, &cerr)
	require.Empty(t, res.Binary, "the binary of the earlier run is not reported")
	after, err := os.ReadFile(binary)
	require.NoError(t, err)
	require.Equal(t, before, after, "and it is not replaced by a partial one")
	require.NoFileExists(t, filepath.Join(f.dir, "out", ".relay.tmp"))
}

func TestUnmetNeedStopsBeforeCompiling(t *testing.T) {
	f := setup(t, "file", "flagdformat", "ofrep") // ofrep resolves flags, but no evaluator is selected
	opts, log := options(t)

	res, err := Run(ctx, f.load(t), opts)

	var nerr *needs.Error
	require.True(t, errors.As(err, &nerr), "%v", err)
	require.Contains(t, nerr.Error(), `service endpoint "ofrep"`)
	require.Empty(t, res.Binary)
	require.NotContains(t, log.String(), "go build")
	require.FileExists(t, filepath.Join(f.dir, "out", "main.go"), "the source stays for inspection")
}

func TestUnusedEvaluatorWarnsAndBuilds(t *testing.T) {
	f := setup(t, "file", "flagdformat", "flagdeval", "flagsync")
	opts, _ := options(t)
	var warnings bytes.Buffer
	opts.Warn = &warnings

	res, err := Run(ctx, f.load(t), opts)

	require.NoError(t, err)
	require.NotEmpty(t, res.Binary)
	require.Contains(t, warnings.String(), `evaluator "flagd"`)
	require.Len(t, res.Warnings, 1)
}

func TestSourceUsesOnlyGeneratedFiles(t *testing.T) {
	f := setup(t, "file", "flagdformat", "flagdeval", "ofrep")
	opts, _ := options(t)
	opts.SkipCompile = true
	_, err := Run(ctx, f.load(t), opts)
	require.NoError(t, err)

	files, err := generate.Render(f.load(t), generate.Options{})
	require.NoError(t, err)
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(f.dir, "out", filepath.FromSlash(name)))
		require.NoError(t, err)
		if name == "go.mod" {
			continue // go mod tidy rewrites it
		}
		require.Equal(t, string(want), string(got), name)
	}
}

func TestCmdModuleWithoutExecuteNamesTheModule(t *testing.T) {
	f := setup(t, "file", "flagdformat", "flagdeval", "ofrep")
	require.NoError(t, os.MkdirAll(filepath.Join(f.dir, "stubs", "mycmd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "stubs", "mycmd", "go.mod"), []byte("module example.test/mycmd\n\ngo 1.26.0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "stubs", "mycmd", "cmd.go"), []byte("package mycmd\n"), 0o644))
	text, err := os.ReadFile(f.manifest)
	require.NoError(t, err)
	text = append(text, []byte("cmd: {module: example.test/mycmd, version: v0.0.0}\n  - {module: example.test/mycmd, path: ./stubs/mycmd}\n")...)
	// replaces is the last key, so the line is appended to it; the cmd entry goes before it
	manifestText := strings.Replace(string(text), "replaces:\n", "cmd: {module: example.test/mycmd, version: v0.0.0}\nreplaces:\n", 1)
	manifestText = strings.Replace(manifestText, "cmd: {module: example.test/mycmd, version: v0.0.0}\n  - {module: example.test/mycmd, path: ./stubs/mycmd}\n", "  - {module: example.test/mycmd, path: ./stubs/mycmd}\n", 1)
	require.NoError(t, os.WriteFile(f.manifest, []byte(manifestText), 0o644))
	opts, _ := options(t)

	res, err := Run(ctx, f.load(t), opts)

	var cerr *gotool.CompileError
	require.ErrorAs(t, err, &cerr)
	require.Len(t, cerr.Components, 1)
	require.Equal(t, "example.test/mycmd", cerr.Components[0].Module)
	require.Contains(t, err.Error(), "Execute")
	require.Empty(t, res.Binary)
}
