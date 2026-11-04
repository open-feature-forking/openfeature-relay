// Package build runs the steps of a build in their order (UC-001, steps 5 to 9): generate the source, resolve the
// component modules, check the versions and the needs of the components, and compile. Each of generate, resolve and
// compile can be skipped on its own (BR-008).
package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/open-feature-forking/openfeature-relay/builder/internal/generate"
	"github.com/open-feature-forking/openfeature-relay/builder/internal/gotool"
	"github.com/open-feature-forking/openfeature-relay/builder/internal/manifest"
	"github.com/open-feature-forking/openfeature-relay/builder/internal/needs"
)

// Options select the steps and what the build stamps.
type Options struct {
	SkipGenerate, SkipGetModules, SkipCompile, SkipStrictVersioning bool
	Generate                                                        generate.Options
	Tool                                                            gotool.Tool
	// Commit is the source revision to stamp; empty means the short revision of the manifest's directory, or "unknown".
	Commit string
	// DistVersion is the version to stamp; empty means the manifest's dist.version.
	DistVersion string
	// Date is the build date to stamp, verbatim; empty means now, as an RFC 3339 time in UTC.
	Date string
	// ProbeOutput, when set, is a file the output of the probe is written to.
	ProbeOutput string
	// Warn receives the warnings; nil discards them.
	Warn io.Writer
}

// Result is what a build produced. Binary is empty unless this run compiled one; a binary left over from an earlier
// run is not reported.
type Result struct {
	SourceDir string
	Binary    string
	Warnings  []string
}

// NoSourceError reports that the generate step was skipped and the output location holds no source (A4).
type NoSourceError struct{ Dir string }

func (e *NoSourceError) Error() string {
	return fmt.Sprintf("the output location %s holds no distribution source to build", e.Dir)
}

// Run builds the distribution of the manifest.
func Run(ctx context.Context, m *manifest.Manifest, opts Options) (Result, error) {
	// the Go Toolchain runs in the source directory, so the paths handed to it are absolute
	sourceDir, err := filepath.Abs(generate.OutputDirOf(m, opts.Generate))
	if err != nil {
		return Result{}, err
	}
	res := Result{SourceDir: sourceDir}
	warn := func(messages ...string) {
		for _, msg := range messages {
			res.Warnings = append(res.Warnings, msg)
			if opts.Warn != nil {
				fmt.Fprintf(opts.Warn, "warning: %s\n", msg)
			}
		}
	}

	if opts.SkipGenerate {
		if !hasSource(res.SourceDir) {
			return res, &NoSourceError{Dir: res.SourceDir}
		}
	} else if _, err := generate.Write(m, opts.Generate); err != nil {
		return res, err
	}

	items := generate.Items(m)
	components := make([]gotool.Component, len(items))
	for i, it := range items {
		components[i] = gotool.Component{Group: string(it.Group), Module: it.Module, Version: it.Version,
			Import: it.Import, Alias: it.Alias, Replaced: it.Replaced}
	}
	if m.Cmd != nil {
		// main.go calls its Execute; a compile error that names runtimecmd concerns this module
		replaced := false
		for _, r := range m.Replaces {
			replaced = replaced || r.Module == m.Cmd.Module
		}
		importPath := m.Cmd.Import
		if importPath == "" {
			importPath = m.Cmd.Module
		}
		components = append(components, gotool.Component{Group: "cmd", Module: m.Cmd.Module, Version: m.Cmd.Version,
			Import: importPath, Alias: "runtimecmd", Replaced: replaced})
	}
	tool := opts.Tool

	if !opts.SkipGetModules {
		if err := tool.Tidy(ctx, res.SourceDir, components); err != nil {
			return res, err
		}
		resolved, err := tool.List(ctx, res.SourceDir)
		if err != nil {
			return res, err
		}
		graph, err := tool.ModGraph(ctx, res.SourceDir)
		if err != nil {
			return res, err
		}
		warnings, err := gotool.CheckVersions(components, resolved, graph, opts.SkipStrictVersioning)
		if err != nil {
			return res, err
		}
		warn(warnings...)
	}

	out, err := probe(ctx, m, opts, tool, res.SourceDir, components)
	if err != nil {
		return res, err
	}
	if opts.ProbeOutput != "" {
		if err := os.WriteFile(opts.ProbeOutput, out, 0o644); err != nil { //nolint:gosec // a report to read
			return res, err
		}
	}
	probed, err := needs.ParseProbe(out)
	if err != nil {
		return res, err
	}
	warnings, err := needs.Check(probed)
	if err != nil {
		return res, err
	}
	warn(warnings...)

	if opts.SkipCompile {
		return res, nil
	}
	binary := filepath.Join(res.SourceDir, m.BinaryName())
	// a failed build must leave neither a partial binary nor a stale one that looks like the result
	temp := filepath.Join(res.SourceDir, "."+m.BinaryName()+".tmp")
	defer os.Remove(temp)
	if err := tool.Build(ctx, res.SourceDir, temp, ldflags(m, opts), components); err != nil {
		return res, err
	}
	if err := os.Rename(temp, binary); err != nil {
		return res, fmt.Errorf("placing the binary: %w", err)
	}
	res.Binary = binary
	return res, nil
}

func hasSource(dir string) bool {
	for _, name := range []string{"go.mod", "main.go"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}

// ldflags stamps what flagd version prints: the distribution's version, the source revision and the build date, each
// as given, or as the Builder defaults them (UC-001 Q10, UC-009 Q3).
func ldflags(m *manifest.Manifest, opts Options) string {
	commit := opts.Commit
	if commit == "" {
		commit = revision(m.Dir())
	}
	date := opts.Date
	if date == "" {
		date = time.Now().UTC().Format(time.RFC3339)
	}
	version := opts.DistVersion
	if version == "" {
		version = m.Dist.Version
	}
	return "-X " + quoteFlag("main.version="+version) + " -X " + quoteFlag("main.commit="+commit) + " -X " + quoteFlag("main.date="+date)
}

// quoteFlag quotes an argument of -ldflags that the Go Toolchain splits on spaces, with the quote the value lacks.
func quoteFlag(arg string) string {
	switch {
	case !strings.ContainsAny(arg, " 	'\""):
		return arg
	case !strings.Contains(arg, "'"):
		return "'" + arg + "'"
	default:
		return "\"" + arg + "\""
	}
}

// revision returns the short revision of the repository that holds dir, or "unknown".
func revision(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	if err != nil || errors.As(err, &exitErr) {
		return "unknown"
	}
	if rev := strings.TrimSpace(string(out)); rev != "" {
		return rev
	}
	return "unknown"
}

// probe runs the probe of the source in a temporary module of its own, so that the source holds only what the Builder
// generates for the distribution.
func probe(ctx context.Context, m *manifest.Manifest, opts Options, tool gotool.Tool, sourceDir string, components []gotool.Component) ([]byte, error) {
	dir, err := os.MkdirTemp("", "relay-probe-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := generate.WriteProbe(m, opts.Generate, sourceDir, dir); err != nil {
		return nil, err
	}
	// the checksums of the source cover what the probe imports
	if sums, err := os.ReadFile(filepath.Join(sourceDir, "go.sum")); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "go.sum"), sums, 0o600); err != nil {
			return nil, err
		}
	}
	if !opts.SkipGetModules {
		if err := tool.Tidy(ctx, dir, components); err != nil {
			return nil, err
		}
	} else {
		// the dependencies recorded with the source stand; the probe's own go.mod is written from them
		base := tool.Env
		if base == nil {
			base = os.Environ()
		}
		tool.Env = append(slices.Clone(base), "GOFLAGS=-mod=mod")
	}
	return tool.Probe(ctx, dir, components)
}
