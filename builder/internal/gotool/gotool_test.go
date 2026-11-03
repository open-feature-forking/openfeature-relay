package gotool

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type proxyModule struct {
	path, version string
	files         map[string]string
}

// writeProxy writes a module proxy that the Go Toolchain reads from disk, so no test needs the network.
func writeProxy(t *testing.T, mods ...proxyModule) string {
	t.Helper()
	root := t.TempDir()
	versions := map[string][]string{}
	for _, m := range mods {
		dir := filepath.Join(root, m.path, "@v")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, content := range m.files {
			w, err := zw.Create(fmt.Sprintf("%s@%s/%s", m.path, m.version, name))
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(content))
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, m.version+".zip"), buf.String())
		write(t, filepath.Join(dir, m.version+".mod"), m.files["go.mod"])
		write(t, filepath.Join(dir, m.version+".info"), fmt.Sprintf(`{"Version":%q,"Time":"2026-01-01T00:00:00Z"}`, m.version))
		versions[m.path] = append(versions[m.path], m.version)
	}
	for path, vs := range versions {
		write(t, filepath.Join(root, path, "@v", "list"), strings.Join(vs, "\n")+"\n")
	}
	return root
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// testTool runs the go binary that runs the tests, against the proxy, with its own module cache.
func testTool(t *testing.T, proxy string, extra ...string) Tool {
	t.Helper()
	env := append(os.Environ(),
		"GOPROXY=file://"+filepath.ToSlash(proxy), "GOSUMDB=off", "GOFLAGS=-modcacherw", "GOTOOLCHAIN=local",
		"GOMODCACHE="+t.TempDir())
	return Tool{Go: filepath.Join(runtime.GOROOT(), "bin", "go"), Env: append(env, extra...)}
}

func goMod(module string, requires map[string]string, replaces map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "module %s\n\ngo 1.26.0\n", module)
	for m, v := range requires {
		fmt.Fprintf(&b, "\nrequire %s %s\n", m, v)
	}
	for m, p := range replaces {
		fmt.Fprintf(&b, "\nreplace %s => %s\n", m, p)
	}
	return b.String()
}

func comp(name string, files map[string]string) proxyModule {
	files["go.mod"] = fmt.Sprintf("module example.test/%s\n\ngo 1.26.0\n", name)
	return proxyModule{path: "example.test/" + name, version: "v1.0.0", files: files}
}

const probeSource = `package main

import "fmt"

func main() { fmt.Println("probe ran for", "host") }
`

// distribution writes a generated source: a main package and a probe over the given requirements.
func distribution(t *testing.T, requires, replaces map[string]string, mainSource string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), goMod("example.test/dist", requires, replaces))
	write(t, filepath.Join(dir, "main.go"), mainSource)
	write(t, filepath.Join(dir, "probe", "main.go"), probeSource)
	return dir
}

func importMain(imports ...string) string {
	var b strings.Builder
	b.WriteString("package main\n\nimport (\n")
	for _, i := range imports {
		fmt.Fprintf(&b, "\t%s\n", i)
	}
	b.WriteString(")\n\nfunc main() {")
	for _, i := range imports {
		alias := strings.Fields(i)[0]
		fmt.Fprintf(&b, " _ = %s.NewFactory()", alias)
	}
	b.WriteString(" }\n")
	return b.String()
}

var ctx = context.Background()

func TestUnresolvableModule(t *testing.T) {
	tool := testTool(t, writeProxy(t))
	dir := distribution(t, map[string]string{"example.test/missing": "v1.2.3"}, nil,
		importMain(`missing "example.test/missing"`))
	components := []Component{{Group: "sync_providers", Module: "example.test/missing", Version: "v1.2.3", Import: "example.test/missing", Alias: "missing"}}

	err := tool.Tidy(ctx, dir, components)

	var resolve *ResolveError
	if !errors.As(err, &resolve) {
		t.Fatalf("want a ResolveError, got %v", err)
	}
	if resolve.Module != "example.test/missing" || resolve.Version != "v1.2.3" || resolve.Reason == "" || resolve.Hint != "" {
		t.Fatalf("the component, its declared version and the toolchain's reason are named: %+v", resolve)
	}
}

func TestMissingReplaceHint(t *testing.T) {
	needsCore := proxyModule{path: "example.test/needscore", version: "v1.0.0", files: map[string]string{
		"go.mod":  goMod("example.test/needscore", map[string]string{"example.test/core": "v0.0.0"}, nil),
		"comp.go": "package needscore\n\nimport _ \"example.test/core\"\n\nfunc NewFactory() string { return \"\" }\n",
	}}
	tool := testTool(t, writeProxy(t, needsCore))
	dir := distribution(t, map[string]string{"example.test/needscore": "v1.0.0"}, nil,
		importMain(`needscore "example.test/needscore"`))
	components := []Component{{Group: "evaluators", Module: "example.test/needscore", Version: "v1.0.0", Import: "example.test/needscore", Alias: "needscore"}}

	err := tool.Tidy(ctx, dir, components)

	var resolve *ResolveError
	if !errors.As(err, &resolve) {
		t.Fatalf("want a ResolveError, got %v", err)
	}
	if resolve.Module != "example.test/core" || !strings.Contains(resolve.Hint, "replaces") || !strings.Contains(resolve.Hint, "example.test/core") {
		t.Fatalf("the relay module that is not replaced is named with the hint: %+v", resolve)
	}
}

// versionsDiffer builds the situation of A8: the manifest declares a at v1.0.0, and b requires a at v1.1.0.
func versionsDiffer(t *testing.T) (Tool, string, []Component) {
	t.Helper()
	aOld := comp("a", map[string]string{"a.go": "package a\n\nfunc NewFactory() string { return \"\" }\n"})
	aNew := aOld
	aNew.version = "v1.1.0"
	b := proxyModule{path: "example.test/b", version: "v1.0.0", files: map[string]string{
		"go.mod": goMod("example.test/b", map[string]string{"example.test/a": "v1.1.0"}, nil),
		"b.go":   "package b\n\nimport \"example.test/a\"\n\nfunc NewFactory() string { return a.NewFactory() }\n",
	}}
	tool := testTool(t, writeProxy(t, aOld, aNew, b))
	dir := distribution(t, map[string]string{"example.test/a": "v1.0.0", "example.test/b": "v1.0.0"}, nil,
		importMain(`a "example.test/a"`, `b "example.test/b"`))
	if err := tool.Tidy(ctx, dir, nil); err != nil {
		t.Fatal(err)
	}
	return tool, dir, []Component{
		{Group: "sync_providers", Module: "example.test/a", Version: "v1.0.0", Import: "example.test/a", Alias: "a"},
		{Group: "evaluators", Module: "example.test/b", Version: "v1.0.0", Import: "example.test/b", Alias: "b"},
	}
}

func TestResolvedVersionDiffers(t *testing.T) {
	tool, dir, components := versionsDiffer(t)
	resolved, err := tool.List(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := tool.ModGraph(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = CheckVersions(components, resolved, graph, false)

	var verr *VersionError
	if !errors.As(err, &verr) || len(verr.Mismatches) != 1 {
		t.Fatalf("want one mismatch, got %v", err)
	}
	m := verr.Mismatches[0]
	if m.Component.Module != "example.test/a" || m.Component.Version != "v1.0.0" || m.Resolved != "v1.1.0" ||
		strings.Join(m.RequiredBy, ",") != "example.test/b@v1.0.0" {
		t.Fatalf("the component, both versions and its requirer are named: %+v", m)
	}
	for _, want := range []string{"example.test/a", "v1.0.0", "v1.1.0", "example.test/b@v1.0.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the report names %s: %v", want, err)
		}
	}
}

func TestSkipStrictVersioningWarns(t *testing.T) {
	tool, dir, components := versionsDiffer(t)
	resolved, _ := tool.List(ctx, dir)
	graph, _ := tool.ModGraph(ctx, dir)

	warnings, err := CheckVersions(components, resolved, graph, true)

	if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "example.test/a") || !strings.Contains(warnings[0], "v1.1.0") {
		t.Fatalf("a difference is a warning that names the module and both versions: %v, %v", warnings, err)
	}
}

func TestReplaceSkipsVersionCheck(t *testing.T) {
	local := t.TempDir()
	write(t, filepath.Join(local, "go.mod"), "module example.test/a\n\ngo 1.26.0\n")
	write(t, filepath.Join(local, "a.go"), "package a\n\nfunc NewFactory() string { return \"\" }\n")
	tool := testTool(t, writeProxy(t))
	dir := distribution(t, map[string]string{"example.test/a": "v0.0.0"}, map[string]string{"example.test/a": local},
		importMain(`a "example.test/a"`))
	components := []Component{{Group: "sync_providers", Module: "example.test/a", Version: "v0.0.0", Import: "example.test/a", Alias: "a", Replaced: true}}
	if err := tool.Tidy(ctx, dir, components); err != nil {
		t.Fatal(err)
	}
	resolved, err := tool.List(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	graph, _ := tool.ModGraph(ctx, dir)

	warnings, err := CheckVersions(components, resolved, graph, false)

	if err != nil || len(warnings) != 0 {
		t.Fatalf("a replaced module is not checked: %v, %v", warnings, err)
	}
}

// compiled writes a distribution over one proxy component and returns it with its tool.
func compiled(t *testing.T, files map[string]string) (Tool, string, []Component) {
	t.Helper()
	c := comp("c", files)
	tool := testTool(t, writeProxy(t, c))
	dir := distribution(t, map[string]string{"example.test/c": "v1.0.0"}, nil, importMain(`c "example.test/c"`))
	components := []Component{{Group: "service_endpoints", Module: "example.test/c", Version: "v1.0.0", Import: "example.test/c", Alias: "c"}}
	return tool, dir, components
}

func TestMissingFactoryFunction(t *testing.T) {
	tool, dir, components := compiled(t, map[string]string{"c.go": "package c\n"})
	// go mod tidy does not compile, so the missing function shows when the source is built
	if err := tool.Tidy(ctx, dir, components); err != nil {
		t.Fatal(err)
	}

	err := tool.Build(ctx, dir, filepath.Join(t.TempDir(), "dist"), "", components)

	var cerr *CompileError
	if !errors.As(err, &cerr) || len(cerr.Components) != 1 || cerr.Components[0].Module != "example.test/c" {
		t.Fatalf("the component without a factory is named: %v", err)
	}
	if !strings.Contains(err.Error(), "NewFactory") {
		t.Fatalf("the toolchain's report is kept: %v", err)
	}
}

func TestCompileErrorNamesComponent(t *testing.T) {
	tool, dir, components := compiled(t, map[string]string{
		"c.go": "package c\n\nfunc NewFactory() string { return notDefined }\n",
	})
	if err := tool.Tidy(ctx, dir, components); err != nil {
		t.Fatal(err)
	}

	err := tool.Build(ctx, dir, filepath.Join(t.TempDir(), "dist"), "", components)

	var cerr *CompileError
	if !errors.As(err, &cerr) || len(cerr.Components) != 1 || cerr.Components[0].Module != "example.test/c" {
		t.Fatalf("the component that fails to compile is named: %v", err)
	}
	if !strings.Contains(err.Error(), "notDefined") {
		t.Fatalf("the toolchain's report is kept: %v", err)
	}
}

func TestCrossBuildRunsProbeOnHost(t *testing.T) {
	other := "linux"
	if runtime.GOOS == "linux" {
		other = "windows"
	}
	otherArch := "arm64"
	if runtime.GOARCH == "arm64" {
		otherArch = "amd64"
	}
	tool, dir, components := compiled(t, map[string]string{"c.go": "package c\n\nfunc NewFactory() string { return \"\" }\n"})
	tool.Env = append(tool.Env, "GOOS="+other, "GOARCH="+otherArch, "GOFIPS140=latest")
	if err := tool.Tidy(ctx, dir, components); err != nil {
		t.Fatal(err)
	}

	out, err := tool.Probe(ctx, dir, components)

	if err != nil || !strings.Contains(string(out), "probe ran for host") {
		t.Fatalf("the probe runs for the host although GOOS and GOARCH name another platform: %q, %v", out, err)
	}
	if err := tool.Build(ctx, dir, filepath.Join(t.TempDir(), "dist"), "", components); err != nil {
		t.Fatalf("the final build keeps the platform: %v", err)
	}
}

func TestBuildIgnoresWorkspace(t *testing.T) {
	tool, dir, components := compiled(t, map[string]string{"c.go": "package c\n\nfunc NewFactory() string { return \"\" }\n"})
	broken := filepath.Join(t.TempDir(), "go.work")
	write(t, broken, "go 1.26.0\n\nuse ./does-not-exist\n")
	tool.Env = append(tool.Env, "GOWORK="+filepath.ToSlash(broken))

	if err := tool.Tidy(ctx, dir, components); err != nil {
		t.Fatalf("tidy ignores the workspace: %v", err)
	}
	output := filepath.Join(t.TempDir(), "dist")
	if err := tool.Build(ctx, dir, output, "-X main.version=1.2.3", components); err != nil {
		t.Fatalf("build ignores the workspace: %v", err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal(err)
	}
}

// TestMissingReplaceHintForAnUnreadableGoMod pins the hint for the report go prints when a module has no release:
// the revision it looks for does not exist.
func TestMissingReplaceHintForAnUnreadableGoMod(t *testing.T) {
	report := "go: downloading example.test/core v0.1.0\ngo: example.test/dist imports\n\texample.test/comp imports\n" +
		"\texample.test/core/util: reading example.test/core/go.mod at revision core/v0.1.0: unknown revision core/v0.1.0"

	err := mapResolve(report, []Component{{Module: "example.test/comp", Version: "v1.0.0"}}, errors.New("exit status 1"))

	var resolve *ResolveError
	if !errors.As(err, &resolve) {
		t.Fatalf("want a ResolveError, got %v", err)
	}
	if resolve.Module != "example.test/core" || !strings.Contains(resolve.Hint, "replaces") {
		t.Fatalf("the module without a release is named with the hint: %+v", resolve)
	}
}
