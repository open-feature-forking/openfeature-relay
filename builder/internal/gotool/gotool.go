// Package gotool runs the Go Toolchain on a generated distribution source and maps what it reports back to the
// components of the manifest. It always runs with GOWORK=off, so a workspace around the output location never
// changes what is built.
package gotool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Component is a component module of the manifest, as the generated source imports it.
type Component struct {
	// Group is the manifest group, such as "sync_providers".
	Group string
	// Module is the module path and Version the version the manifest declares.
	Module  string
	Version string
	// Import is the package that holds the component's factory, and Alias the identifier the generated source
	// uses for it.
	Import string
	Alias  string
	// Replaced marks a module the manifest replaces by a local path.
	Replaced bool
}

func (c Component) String() string {
	return fmt.Sprintf("%s %s (%s)", c.Module, c.Version, c.Group)
}

// Tool runs the go binary.
type Tool struct {
	// Go is the go binary; empty means "go" on PATH.
	Go string
	// Env is the environment of every call; nil means the process environment.
	Env []string
	// Log receives the command lines and the output of the calls when set.
	Log io.Writer
}

func (t Tool) run(ctx context.Context, dir string, env []string, args ...string) ([]byte, []byte, error) {
	bin := t.Go
	if bin == "" {
		bin = "go"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if t.Log != nil {
		fmt.Fprintf(t.Log, "go %s\n", strings.Join(args, " "))
	}
	err := cmd.Run()
	if t.Log != nil {
		_, _ = t.Log.Write(stderr.Bytes())
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

// env returns the environment of a call: the tool's, with GOWORK=off and, unless the environment names one, the
// default CGO_ENABLED=0 of today's release build. unset names variables that are removed.
func (t Tool) env(unset ...string) []string {
	base := t.Env
	if base == nil {
		base = os.Environ()
	}
	out := make([]string, 0, len(base)+2)
	hasCGO := false
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case name == "GOWORK":
		case slices.Contains(unset, name):
		default:
			hasCGO = hasCGO || name == "CGO_ENABLED"
			out = append(out, kv)
		}
	}
	out = append(out, "GOWORK=off")
	if !hasCGO {
		out = append(out, "CGO_ENABLED=0")
	}
	return out
}

// ResolveError reports a module the Go Toolchain could not resolve.
type ResolveError struct {
	Module string
	// Version is the declared version; empty when the module is not a component of the manifest.
	Version string
	// Reason is what the Go Toolchain reported.
	Reason string
	// TestedBy names the component whose tests import the module, when only they need it: go mod tidy resolves the
	// imports of the components' tests too.
	TestedBy string
	// Hint says what to change, when the Builder knows.
	Hint string
}

func (e *ResolveError) Error() string {
	var b strings.Builder
	switch {
	case e.Version != "":
		fmt.Fprintf(&b, "cannot resolve the component module %s %s: %s", e.Module, e.Version, e.Reason)
	case e.TestedBy != "":
		fmt.Fprintf(&b, "cannot resolve the module %s, which the tests of the component module %s import: %s", e.Module, e.TestedBy, e.Reason)
	default:
		fmt.Fprintf(&b, "cannot resolve the module %s, which a component module requires: %s", e.Module, e.Reason)
	}
	if e.Hint != "" {
		b.WriteString("\n" + e.Hint)
	}
	return b.String()
}

// Tidy runs go mod tidy in dir, which resolves every module the generated source needs. An unresolvable module is
// reported as a *ResolveError, naming the component when it is one.
func (t Tool) Tidy(ctx context.Context, dir string, components []Component) error {
	_, stderr, err := t.run(ctx, dir, t.env(), "mod", "tidy")
	if err == nil {
		return nil
	}
	return mapResolve(string(stderr), components, err)
}

func mapResolve(stderr string, components []Component, runErr error) error {
	reason := strings.TrimSpace(stderr)
	if reason == "" {
		reason = runErr.Error()
	}
	// the failing module is the one named last, as "module@version: reason" or as the go.mod it could not read; the
	// ones before it only require it
	var line, module string
	if found := failing.FindAllStringSubmatch(reason, -1); len(found) > 0 {
		line, module = strings.TrimSpace(found[len(found)-1][0]), found[len(found)-1][1]
	}
	if found := unreadable.FindAllStringSubmatch(reason, -1); len(found) > 0 {
		line, module = strings.TrimSpace(found[len(found)-1][0]), found[len(found)-1][1]
	}
	if module == "" {
		return fmt.Errorf("go mod tidy failed: %s", reason)
	}
	for _, c := range components {
		if c.Module == module {
			return &ResolveError{Module: c.Module, Version: c.Version, Reason: line}
		}
	}
	e := &ResolveError{Module: module, Reason: line}
	who := "a component module requires " + module
	if c, ok := testedBy(reason, components); ok {
		e.TestedBy = c.Module
		who = fmt.Sprintf("the tests of the component module %s import %s (go mod tidy resolves the imports of the components' tests too)", c.Module, module)
	}
	e.Hint = fmt.Sprintf("%s, which the Go Toolchain cannot fetch. A manifest in a repository replaces every relay module of its "+
		"build list by a local path, not only the components: add `- {module: %s, path: <path to the module>}` to `replaces`.",
		who, module)
	return e
}

var (
	failing    = regexp.MustCompile(`(?m)(\S+?)@(v[^\s:]+): .*$`)
	unreadable = regexp.MustCompile(`(?m)^.*: reading (\S+?)/go\.mod at revision .*$`)
	// "pkg tested by" in the import chain: the package whose test binary needs what follows
	testedByLine = regexp.MustCompile(`(?m)^\s*(\S+) tested by$`)
)

// testedBy returns the component whose package's tests the import chain in reason names.
func testedBy(reason string, components []Component) (Component, bool) {
	for _, m := range testedByLine.FindAllStringSubmatch(reason, -1) {
		pkg := m[1]
		for _, c := range components {
			if pkg == c.Import || pkg == c.Module || strings.HasPrefix(pkg, c.Module+"/") {
				return c, true
			}
		}
	}
	return Component{}, false
}

// Module is one entry of the build list.
type Module struct {
	Path    string
	Version string
	Main    bool
	Replace *Module
}

// List returns the build list of the module in dir, as go list -m -json all reports it.
func (t Tool) List(ctx context.Context, dir string) ([]Module, error) {
	stdout, stderr, err := t.run(ctx, dir, t.env(), "list", "-m", "-json", "all")
	if err != nil {
		return nil, fmt.Errorf("go list -m failed: %s", strings.TrimSpace(string(stderr)))
	}
	var modules []Module
	dec := json.NewDecoder(bytes.NewReader(stdout))
	for {
		var m Module
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				return modules, nil
			}
			return nil, fmt.Errorf("go list -m printed unreadable output: %w", err)
		}
		modules = append(modules, m)
	}
}

// Graph maps each module version of the build list to the module versions that require it.
type Graph map[string][]string

// Requirers returns the module versions, as "path@version", that require module at version, sorted.
func (g Graph) Requirers(module, version string) []string {
	var out []string
	for _, from := range g[module+"@"+version] {
		// the main module has no version
		if strings.Contains(from, "@") {
			out = append(out, from)
		}
	}
	sort.Strings(out)
	return out
}

// ModGraph returns the requirement graph of the module in dir, as go mod graph reports it.
func (t Tool) ModGraph(ctx context.Context, dir string) (Graph, error) {
	stdout, stderr, err := t.run(ctx, dir, t.env(), "mod", "graph")
	if err != nil {
		return nil, fmt.Errorf("go mod graph failed: %s", strings.TrimSpace(string(stderr)))
	}
	g := Graph{}
	for _, line := range strings.Split(string(stdout), "\n") {
		from, to, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok {
			g[to] = append(g[to], from)
		}
	}
	return g, nil
}

// VersionError reports components that resolved at another version than the manifest declares.
type VersionError struct {
	Mismatches []Mismatch
}

// Mismatch is one component whose resolved version differs from the declared one.
type Mismatch struct {
	Component Component
	Resolved  string
	// RequiredBy are the module versions that require the resolved version.
	RequiredBy []string
}

func (e *VersionError) Error() string {
	lines := make([]string, 0, len(e.Mismatches))
	for _, m := range e.Mismatches {
		lines = append(lines, fmt.Sprintf("the component module %s resolved at %s, but the manifest declares %s; required at %s by: %s",
			m.Component.Module, m.Resolved, m.Component.Version, m.Resolved, strings.Join(m.RequiredBy, ", ")))
	}
	return strings.Join(lines, "\n")
}

// CheckVersions compares the resolved version of every component with the declared one. A replaced module is not
// checked. A difference is an error, or a warning when skipStrict is set.
func CheckVersions(components []Component, resolved []Module, graph Graph, skipStrict bool) (warnings []string, err error) {
	byPath := make(map[string]Module, len(resolved))
	for _, m := range resolved {
		byPath[m.Path] = m
	}
	var verr VersionError
	for _, c := range components {
		m, ok := byPath[c.Module]
		if !ok || c.Replaced || m.Replace != nil || m.Version == c.Version {
			continue
		}
		verr.Mismatches = append(verr.Mismatches, Mismatch{Component: c, Resolved: m.Version, RequiredBy: graph.Requirers(c.Module, m.Version)})
	}
	if len(verr.Mismatches) == 0 {
		return nil, nil
	}
	if skipStrict {
		return strings.Split(verr.Error(), "\n"), nil
	}
	return nil, &verr
}

// CompileError reports a failed compilation with the components it concerns.
type CompileError struct {
	// Output is what the Go Toolchain printed.
	Output string
	// Components are the components the output names: by their package, or by the identifier the generated source
	// uses for them.
	Components []Component
}

func (e *CompileError) Error() string {
	var b strings.Builder
	b.WriteString("the Go Toolchain cannot compile the distribution source")
	if len(e.Components) > 0 {
		names := make([]string, len(e.Components))
		for i, c := range e.Components {
			names[i] = c.String()
		}
		b.WriteString(", which concerns " + strings.Join(names, ", "))
	}
	b.WriteString(":\n" + strings.TrimSpace(e.Output))
	return b.String()
}

func mapCompile(output string, components []Component) *CompileError {
	e := &CompileError{Output: output}
	for _, c := range components {
		if concerns(output, c) {
			e.Components = append(e.Components, c)
		}
	}
	return e
}

// concerns reports whether the output names the component: a "# <package>" header of its packages, or a
// reference through the alias of the generated source.
func concerns(output string, c Component) bool {
	for _, line := range strings.Split(output, "\n") {
		if pkg, ok := strings.CutPrefix(line, "# "); ok {
			pkg, _, _ = strings.Cut(pkg, " ")
			if pkg == c.Import || strings.HasPrefix(pkg, c.Module+"/") || pkg == c.Module {
				return true
			}
		}
		if c.Alias != "" && (strings.Contains(line, ": undefined: "+c.Alias+".") || strings.Contains(line, " "+c.Alias+".")) {
			return true
		}
	}
	return false
}

// Probe runs ./probe in dir for the host: GOOS, GOARCH and GOFIPS140 are unset, so a cross-build still runs it. It
// returns what the probe printed.
func (t Tool) Probe(ctx context.Context, dir string, components []Component) ([]byte, error) {
	stdout, stderr, err := t.run(ctx, dir, t.env("GOOS", "GOARCH", "GOFIPS140"), "run", "./probe")
	if err != nil {
		return nil, mapRun(string(stderr), components)
	}
	return stdout, nil
}

// Build compiles the distribution in dir into output, with the given linker flags. GOOS, GOARCH, GOFLAGS and
// GOFIPS140 of the environment apply.
func (t Tool) Build(ctx context.Context, dir, output string, ldflags string, components []Component) error {
	args := []string{"build", "-o", output}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	_, stderr, err := t.run(ctx, dir, t.env(), append(args, ".")...)
	if err != nil {
		return mapRun(string(stderr), components)
	}
	return nil
}

func mapRun(stderr string, components []Component) error {
	if strings.HasPrefix(strings.TrimSpace(stderr), "go: ") && !strings.Contains(stderr, "\n# ") && !strings.HasPrefix(stderr, "# ") {
		if strings.Contains(stderr, "no required module provides package") || strings.Contains(stderr, "cannot find module") {
			return mapResolve(stderr, components, errors.New("resolving"))
		}
	}
	return mapCompile(stderr, components)
}
