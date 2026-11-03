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
	// Date is the build date to stamp; the zero value means now.
	Date time.Time
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
	res := Result{SourceDir: generate.OutputDir(m)}
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

	out, err := tool.Probe(ctx, res.SourceDir, components)
	if err != nil {
		return res, err
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

// ldflags stamps what flagd version prints today: the distribution's version, the short source revision and the
// build date (Q10).
func ldflags(m *manifest.Manifest, opts Options) string {
	commit := opts.Commit
	if commit == "" {
		commit = revision(m.Dir())
	}
	date := opts.Date
	if date.IsZero() {
		date = time.Now()
	}
	return fmt.Sprintf("-X main.version=%s -X main.commit=%s -X main.date=%s",
		m.Dist.Version, commit, date.UTC().Format(time.RFC3339))
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
