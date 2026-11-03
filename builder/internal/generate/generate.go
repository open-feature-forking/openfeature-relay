// Package generate writes the source of a distribution from its manifest: a main module with the main package, the
// components package that wires the factories, the probe that reports them, and go.mod. It writes these files and no
// other (BR-009), and its output is the same for the same manifest.
package generate

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/builder/internal/manifest"
)

// Module paths of the relay modules every distribution requires.
const (
	CoreModule    = "github.com/open-feature-forking/openfeature-relay/core"
	RuntimeModule = "github.com/open-feature-forking/openfeature-relay/runtime"
)

// Options are what the Builder supplies besides the manifest.
type Options struct {
	// RuntimeVersion is the version of the runtime module when the manifest does not pin one, and CoreVersion that of
	// the core module. Both default to v0.0.0, which a manifest in a repository resolves through its replaces.
	RuntimeVersion string
	CoreVersion    string
	// GoVersion is the go directive of the generated module; it defaults to 1.27.
	GoVersion string
}

// Item is a component of the manifest as the generated source refers to it.
type Item struct {
	Group manifest.Group
	// Module and Version are the component module; Import is the package that holds its factory.
	Module  string
	Version string
	Import  string
	// Alias is the identifier the generated source imports the package as.
	Alias string
	// Replaced is set when the manifest replaces the module by a local path.
	Replaced bool
}

var aliasPrefix = map[manifest.Group]string{
	manifest.SyncProviders:        "sync",
	manifest.ConfigurationFormats: "format",
	manifest.Evaluators:           "evaluator",
	manifest.Engines:              "engine",
	manifest.ServiceEndpoints:     "endpoint",
	manifest.Telemetry:            "telemetry",
	manifest.Middleware:           "middleware",
	manifest.EventSinks:           "eventsink",
}

// Items returns the components of the manifest in manifest order, with the alias each has in the generated source.
func Items(m *manifest.Manifest) []Item {
	replaced := map[string]bool{}
	for _, r := range m.Replaces {
		replaced[r.Module] = true
	}
	var items []Item
	for _, e := range m.Entries() {
		path := e.Entry.Import
		if path == "" {
			path = e.Entry.Module
		}
		items = append(items, Item{
			Group: e.Group, Module: e.Entry.Module, Version: e.Entry.Version, Import: path,
			Alias: fmt.Sprintf("%s%d", aliasPrefix[e.Group], e.Index), Replaced: replaced[e.Entry.Module],
		})
	}
	return items
}

// OutputDir is the directory the source is written to: the manifest's output_path, read relative to the manifest.
func OutputDir(m *manifest.Manifest) string {
	if filepath.IsAbs(m.Dist.OutputPath) {
		return filepath.Clean(m.Dist.OutputPath)
	}
	return filepath.Join(m.Dir(), m.Dist.OutputPath)
}

// Files are the generated files by their path in the output directory, with the slash as separator.
type Files map[string][]byte

// Render returns the files of the manifest's source.
func Render(m *manifest.Manifest, opts Options) (Files, error) {
	if opts.RuntimeVersion == "" {
		opts.RuntimeVersion = "v0.0.0"
	}
	if opts.CoreVersion == "" {
		opts.CoreVersion = "v0.0.0"
	}
	if opts.GoVersion == "" {
		opts.GoVersion = "1.27"
	}
	items := Items(m)
	data := templateData{
		Module: m.ModulePath(), Name: m.Dist.Name, Items: items,
		CoreModule: CoreModule, RuntimeModule: RuntimeModule, GoVersion: opts.GoVersion,
		CmdImport: RuntimeModule + "/cmd",
	}
	if m.Cmd != nil {
		data.CmdImport = m.Cmd.Import
		if data.CmdImport == "" {
			data.CmdImport = m.Cmd.Module
		}
	}
	requires := map[string]string{CoreModule: opts.CoreVersion}
	if m.Cmd == nil {
		requires[RuntimeModule] = opts.RuntimeVersion
	} else {
		requires[m.Cmd.Module] = m.Cmd.Version
	}
	if m.Runtime != nil {
		requires[RuntimeModule] = m.Runtime.Version
	}
	for _, it := range items {
		requires[it.Module] = it.Version
	}
	for module, version := range requires {
		data.Requires = append(data.Requires, moduleVersion{module, version})
	}
	sort.Slice(data.Requires, func(i, j int) bool { return data.Requires[i].Module < data.Requires[j].Module })

	out, err := filepath.Abs(OutputDir(m))
	if err != nil {
		return nil, err
	}
	for _, r := range m.Replaces {
		data.Replaces = append(data.Replaces, moduleVersion{r.Module, relativeTo(out, m.ReplacePath(r))})
	}
	sort.Slice(data.Replaces, func(i, j int) bool { return data.Replaces[i].Module < data.Replaces[j].Module })

	files := Files{}
	for name, tmpl := range templates {
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("rendering %s: %w", name, err)
		}
		content := buf.Bytes()
		if strings.HasSuffix(name, ".go") {
			if content, err = format.Source(content); err != nil {
				return nil, fmt.Errorf("formatting %s: %w", name, err)
			}
		}
		files[name] = content
	}
	return files, nil
}

// Write renders the source and writes it to the output directory. Files that already exist there under other names
// stay as they are.
func Write(m *manifest.Manifest, opts Options) (dir string, err error) {
	files, err := Render(m, opts)
	if err != nil {
		return "", err
	}
	dir = OutputDir(m)
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", fmt.Errorf("creating the output location: %w", err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil { //nolint:gosec // the source is meant to be read
			return "", fmt.Errorf("writing %s: %w", name, err)
		}
	}
	return dir, nil
}

// relativeTo returns target as a path relative to dir, with slashes; a target on another volume stays absolute.
func relativeTo(dir, target string) string {
	abs, err := filepath.Abs(target)
	if err != nil {
		return filepath.ToSlash(target)
	}
	rel, err := filepath.Rel(dir, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	rel = filepath.ToSlash(rel)
	if rel != "." && !strings.HasPrefix(rel, "../") {
		rel = "./" + rel
	}
	return rel
}

type moduleVersion struct{ Module, Version string }

type templateData struct {
	Module, Name, GoVersion   string
	CoreModule, RuntimeModule string
	// CmdImport is the package whose Execute function is the command line of the distribution.
	CmdImport          string
	Items              []Item
	Requires, Replaces []moduleVersion
}
