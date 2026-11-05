package release

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testVersion is the version every simulated release publishes. The release tag of a component module is
// <module directory>/<testVersion> (UC-012 BR-001); nothing is tagged, the module proxy below serves what the tag would.
const testVersion = "v0.0.1-test"

// flagdModule is the module of the flagd binary, which keeps the upstream module path. runtime requires it for its tests,
// so a runtime release can only be consumed when a flagd module is served too (see TestRuntimeRequiresTheFlagdModule).
const flagdModule = "github.com/open-feature/flagd/flagd"

// representatives are the component modules the pinned manifest selects: one of each component type that has a module
// in the repository, with the group of the manifest they go under.
var representatives = []struct{ dir, group string }{
	{"sync/file", "sync_providers"},
	{"service/ofrep", "service_endpoints"},
	{"contrib/flagsmith/middleware", "middleware"},
	{"telemetry/otel", "telemetry"},
	{"evaluator/flagd", "evaluators"},
	{"configformat/flagd", "configuration_formats"},
	{"eventsink/webhook", "event_sinks"},
}

var (
	requirePattern = regexp.MustCompile(`^(\s*(?:require\s+)?)(` + regexp.QuoteMeta(repositoryPath) + `(?:/\S+)?|` + regexp.QuoteMeta(flagdModule) + `)\s+v\S+(.*)$`)
	dirsOnce       sync.Once
	dirsResult     map[string]string
)

// componentDirs maps the directory of every component module of the working tree, as a release tags it, to the content
// of its go.mod.
func componentDirs(t *testing.T) map[string]string {
	t.Helper()
	dirsOnce.Do(func() {
		dirsResult = map[string]string{}
		base := root(t)
		require.NoError(t, filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == ".claude" || (filepath.Dir(p) == base && notComponents[d.Name()]) {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(base, filepath.Dir(p))
			if d.Name() != "go.mod" || rel == "." {
				return nil
			}
			data, err := os.ReadFile(p)
			dirsResult[filepath.ToSlash(rel)] = string(data)
			return err
		}))
	})
	require.NotEmpty(t, dirsResult)
	return dirsResult
}

// releasedGoMod is the go.mod a release publishes for a module: the requirements on other modules of the repository
// name the version released with it (BR-005), and the replace directives, which the go command ignores in a dependency,
// are dropped.
func releasedGoMod(src string) string {
	var out []string
	inReplace := false
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case inReplace:
			inReplace = trimmed != ")"
			continue
		case strings.HasPrefix(trimmed, "replace ("):
			inReplace = true
			continue
		case strings.HasPrefix(trimmed, "replace "):
			continue
		}
		out = append(out, requirePattern.ReplaceAllString(line, "${1}${2} "+testVersion+"${3}"))
	}
	return strings.Join(out, "\n")
}

// moduleZip packs the module in dir the way the go command expects: every file below <module>@<version>/, without the
// modules nested below it, version control metadata and vendored code, and with the go.mod that is published.
func moduleZip(t *testing.T, dir, module, gomod string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	prefix := module + "@" + testVersion + "/"
	write := func(name string, data []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: prefix + name, Method: zip.Deflate, Modified: time.Unix(0, 0)})
		require.NoError(t, err)
		_, err = w.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			if _, nested := os.Stat(filepath.Join(p, "go.mod")); nested == nil || d.Name() == ".git" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "go.mod" || !d.Type().IsRegular() || strings.HasSuffix(rel, "-coverage.out") {
			return nil
		}
		data, err := os.ReadFile(p)
		write(rel, data)
		return err
	}))
	write("go.mod", []byte(gomod))
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// publish writes a module proxy in the layout of `go mod download`, <module>/@v/<version>.{info,mod,zip}, with every
// component module of the working tree at testVersion, and returns its directory. mutate changes the go.mod a module
// is published with, by its directory. lists adds the version list and the latest version of every module, as a proxy serves them; without them a
// module that does not require what its packages import cannot be completed from a newer release.
func publish(t *testing.T, lists bool, mutate map[string]func(gomod string) string) string {
	t.Helper()
	proxy := t.TempDir()
	dirs := map[string]string{}
	for dir, src := range componentDirs(t) {
		dirs[dir] = src
	}
	flagd, err := os.ReadFile(filepath.Join(root(t), "flagd", "go.mod"))
	require.NoError(t, err)
	dirs["flagd"] = string(flagd)
	for dir, src := range dirs {
		module := path.Join(repositoryPath, dir)
		if dir == "flagd" {
			module = flagdModule
		}
		gomod := releasedGoMod(src)
		if f := mutate[dir]; f != nil {
			gomod = f(gomod)
		}
		info, err := json.Marshal(map[string]any{
			"Version": testVersion, "Time": "2026-01-01T00:00:00Z",
			"Origin": map[string]string{"VCS": "git", "Ref": "refs/tags/" + dir + "/" + testVersion},
		})
		require.NoError(t, err)
		base := filepath.Join(proxy, filepath.FromSlash(module), "@v")
		require.NoError(t, os.MkdirAll(base, 0o755))
		for ext, data := range map[string][]byte{
			".info": info, ".mod": []byte(gomod), ".zip": moduleZip(t, filepath.Join(root(t), filepath.FromSlash(dir)), module, gomod),
		} {
			require.NoError(t, os.WriteFile(filepath.Join(base, testVersion+ext), data, 0o644))
		}
		if lists {
			require.NoError(t, os.WriteFile(filepath.Join(base, "list"), []byte(testVersion+"\n"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(base, "..", "@latest"), info, 0o644))
		}
	}
	return proxy
}

// fileURL is the file:// URL of a directory.
func fileURL(dir string) string {
	slashed := filepath.ToSlash(dir)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

// goTool runs commands with an environment in which the go command reads the proxy and the module cache and nothing
// else: outside any workspace, with no checksum database, and with no route to the network.
type goTool struct {
	env []string
}

// hostGo runs go with the environment of the test, in dir.
func hostGo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return string(out)
}

// newGoTool returns the tool for a proxy. The tests skip without a Go toolchain, and when the third-party modules of
// the build are neither in the module cache nor downloadable: the build itself runs offline, so the cache is filled
// first, which costs nothing when it holds them.
func newGoTool(t *testing.T, proxy string) goTool {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no Go toolchain on PATH")
	}
	warmModuleCache(t)
	hostCache := strings.TrimSpace(hostGo(t, root(t), "env", "GOMODCACHE"))
	thirdParty := thirdPartyProxy(t, filepath.Join(hostCache, "cache", "download"))
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "GOFLAGS", "GOPROXY", "GOSUMDB", "GONOSUMDB", "GONOSUMCHECK", "GOPRIVATE", "GONOPROXY", "GOINSECURE", "GOWORK", "GOVCS",
			"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "GOMODCACHE":
		default:
			env = append(env, kv)
		}
	}
	env = append(env,
		"GOWORK=off", "GOMODCACHE="+t.TempDir(), "GOFLAGS=-mod=mod -modcacherw", "GOSUMDB=off", "GOVCS=*:off",
		"GOPROXY="+fileURL(proxy)+","+fileURL(thirdParty),
		"HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1",
	)
	return goTool{env: env}
}

// thirdPartyProxy is a view of the download directory of the host's module cache without the modules of this repository,
// which the host may hold from other runs: the proxy of the release is the only source of those. The tests use a module
// cache of their own, so nothing they fetch reaches the host's.
func thirdPartyProxy(t *testing.T, download string) string {
	t.Helper()
	view := t.TempDir()
	link := func(from, to string) {
		if err := os.Symlink(from, to); err != nil {
			t.Skipf("no symbolic links to build a view of the module cache: %v", err)
		}
	}
	entries, err := os.ReadDir(download)
	require.NoError(t, err)
	for _, e := range entries {
		if e.Name() != "github.com" {
			link(filepath.Join(download, e.Name()), filepath.Join(view, e.Name()))
		}
	}
	require.NoError(t, os.MkdirAll(filepath.Join(view, "github.com"), 0o755))
	owners, err := os.ReadDir(filepath.Join(download, "github.com"))
	require.NoError(t, err)
	for _, e := range owners {
		if e.Name() != "open-feature-forking" {
			link(filepath.Join(download, "github.com", e.Name()), filepath.Join(view, "github.com", e.Name()))
		}
	}
	return view
}

var warmOnce sync.Once

// warmModuleCache downloads the modules the dependencies of the build resolve to.
func warmModuleCache(t *testing.T) {
	t.Helper()
	dirs := []string{"core", "runtime", "builder"}
	for _, r := range representatives {
		dirs = append(dirs, r.dir)
	}
	warmOnce.Do(func() {
		for _, dir := range dirs {
			cmd := exec.Command("go", "mod", "download")
			cmd.Dir = filepath.Join(root(t), dir)
			cmd.Env = append(os.Environ(), "GOWORK=off")
			if out, err := cmd.CombinedOutput(); err != nil {
				warmErr = fmt.Errorf("the module cache lacks the dependencies of %s and cannot be filled: %w\n%s", dir, err, out)
				return
			}
		}
	})
	if warmErr != nil {
		t.Skip(warmErr)
	}
}

var warmErr error

// run runs a command in dir with the hermetic environment and returns its combined output.
func (g goTool) run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = g.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// buildBuilder compiles relay-builder the way a release does, stamped with the version the release creates, which is
// the version of the core and runtime modules that a manifest does not pin.
func buildBuilder(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "relay-builder")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	hostGo(t, filepath.Join(root(t), "builder"), "build", "-ldflags", "-X main.version="+testVersion, "-o", bin, "./cmd/relay-builder")
	return bin
}

// manifest pins the representative component modules, and the runtime, at testVersion, with no replaces.
func manifest(output string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "dist:\n  name: publish-sim\n  version: 0.0.1\n  output_path: %s\n  module: example.test/publish-sim\n  binary: publish-sim\n", filepath.ToSlash(output))
	for _, r := range representatives {
		entry := fmt.Sprintf("{module: %s/%s, version: %s}", repositoryPath, r.dir, testVersion)
		if r.group == "telemetry" {
			fmt.Fprintf(&b, "telemetry: %s\n", entry)
		} else {
			fmt.Fprintf(&b, "%s:\n  - %s\n", r.group, entry)
		}
	}
	fmt.Fprintf(&b, "runtime: {module: %s/runtime, version: %s}\n", repositoryPath, testVersion)
	return b.String()
}

// runBuilder builds the pinned manifest with relay-builder and returns what it printed and where it put the source and
// the binary.
func runBuilder(t *testing.T, tool goTool, builder string) (output, dist string, err error) {
	t.Helper()
	dir := t.TempDir()
	dist = filepath.Join(dir, "dist")
	manifestFile := filepath.Join(dir, "manifest.yaml")
	require.NoError(t, os.WriteFile(manifestFile, []byte(manifest(dist)), 0o644))
	output, err = tool.run(dir, builder, "--verbose", "--manifest", manifestFile, "--output", dist)
	return output, dist, err
}

// TestPublishedComponentModulesBuildADistribution simulates the release of the component modules and the consumption of
// that release (UC-012 steps 6-7, BR-004, BR-005, and the publishing step of the extension use cases): every component
// module of the working tree is served by a module proxy at <module directory>/<version>, a manifest that pins one
// component module of each type and has no replaces is built against the proxy outside any workspace, and the built
// distribution lists the pinned versions.
func TestPublishedComponentModulesBuildADistribution(t *testing.T) {
	tool := newGoTool(t, publish(t, true, nil))
	builder := buildBuilder(t)

	require.NotContains(t, manifest("x"), "replaces")
	out, dist, err := runBuilder(t, tool, builder)
	require.NoError(t, err, out)

	gomod, err := os.ReadFile(filepath.Join(dist, "go.mod"))
	require.NoError(t, err)
	require.NotContains(t, string(gomod), "replace", "no module of the build list is resolved from a path")

	binary := filepath.Join(dist, "publish-sim")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	listing, err := tool.run(dist, binary, "components", "--output", "json")
	require.NoError(t, err, listing)
	var components struct {
		Runtime struct {
			Version string `json:"version"`
		} `json:"runtime"`
		Components map[string][]struct {
			Module  string `json:"module"`
			Version string `json:"version"`
		} `json:"components"`
	}
	require.NoError(t, json.Unmarshal([]byte(listing), &components), listing)

	got := map[string]string{}
	for _, group := range components.Components {
		for _, c := range group {
			got[c.Module] = c.Version
		}
	}
	want := map[string]string{}
	for _, r := range representatives {
		want[repositoryPath+"/"+r.dir] = testVersion
	}
	require.Equal(t, want, got, "components lists the pinned modules at the pinned versions")
	require.Equal(t, testVersion, components.Runtime.Version)
}

// consume resolves the module in a module of its own that requires nothing else, as a consumer who runs go get does.
func consume(t *testing.T, tool goTool, module string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	gomod := fmt.Sprintf("module example.test/consumer\n\ngo 1.27\n\nrequire %s %s\n", module, testVersion)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(fmt.Sprintf("package main\n\nimport _ %q\n\nfunc main() {}\n", module)), 0o644))
	return tool.run(dir, "go", "mod", "tidy")
}

// TestEveryPublishedModuleResolvesAlone: each representative module, published as above, resolves for a consumer that
// requires only that module, so its go.mod names everything its packages import.
func TestEveryPublishedModuleResolvesAlone(t *testing.T) {
	tool := newGoTool(t, publish(t, false, nil))
	for _, r := range representatives {
		t.Run(r.dir, func(t *testing.T) {
			out, err := consume(t, tool, repositoryPath+"/"+r.dir)
			require.NoError(t, err, out)
		})
	}
}

// TestUnresolvableModulesAreReported (UC-012 A4, seen from the consumer): a module published with a path other than the
// one its tag directory gives, or without the requirement its packages need, does not resolve, and the failure names it.
func TestUnresolvableModulesAreReported(t *testing.T) {
	core := regexp.MustCompile(`(?m)^.*openfeature-relay/core v\S+.*\n`)
	t.Run("module path does not match the tag directory", func(t *testing.T) {
		tool := newGoTool(t, publish(t, true, map[string]func(string) string{"sync/file": func(gomod string) string {
			return strings.Replace(gomod, "module "+repositoryPath+"/sync/file", "module "+repositoryPath+"/sync/wrong", 1)
		}}))
		out, _, err := runBuilder(t, tool, buildBuilder(t))
		require.Error(t, err, out)
		require.Contains(t, out, repositoryPath+"/sync/file")
		require.Contains(t, out, "parsing go.mod")
	})
	t.Run("missing require", func(t *testing.T) {
		tool := newGoTool(t, publish(t, false, map[string]func(string) string{"sync/file": func(gomod string) string {
			return core.ReplaceAllString(gomod, "")
		}}))
		out, err := consume(t, tool, repositoryPath+"/sync/file")
		require.Error(t, err, out)
		require.Contains(t, out, repositoryPath+"/core")
	})
}

// TestRuntimeRequiresTheFlagdModule pins an accepted gap of the release: the tests of the runtime module import the
// flagd module, which keeps the upstream module path github.com/open-feature/flagd/flagd and which no tag of this
// repository can resolve to, so a runtime release can be consumed only where something serves that module (the proxy
// above does). When the runtime no longer requires it, drop the flagd module from publish and this test.
func TestRuntimeRequiresTheFlagdModule(t *testing.T) {
	require.Contains(t, componentDirs(t)["runtime"], flagdModule+" v")
}
