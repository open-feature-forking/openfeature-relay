package manifest

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// semver accepts the version of a distribution, with or without the leading v.
	semver = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	// exactVersion accepts the version of a module: an exact semantic version or a pseudo-version, with the v.
	exactVersion = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	modulePath   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]*(/[A-Za-z0-9._~-]+)*$`)
	distName     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	binaryName   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// DefaultBinary is the name of the binary when the manifest names none: the name of today's flagd.
const DefaultBinary = "flagd"

// ComponentsHintOn reports whether the distribution's startup errors point to the components command: they do unless
// the manifest sets components_hint to false.
func (m *Manifest) ComponentsHintOn() bool { return m.Dist.ComponentsHint != "false" }

// BinaryName returns the name of the binary the manifest asks for.
func (m *Manifest) BinaryName() string {
	if m.Dist.Binary != "" {
		return m.Dist.Binary
	}
	return DefaultBinary
}

// ModulePath returns the module path of the generated source: the manifest's, else the distribution's name.
func (m *Manifest) ModulePath() string {
	if m.Dist.Module != "" {
		return m.Dist.Module
	}
	return m.Dist.Name
}

// Dir is the directory of the manifest, which the replaces are read relative to.
func (m *Manifest) Dir() string { return filepath.Dir(m.File) }

// ReplacePath returns the directory a replace points to, resolved against the manifest's directory.
func (m *Manifest) ReplacePath(r Replace) string {
	if filepath.IsAbs(r.Path) {
		return filepath.Clean(r.Path)
	}
	return filepath.Join(m.Dir(), r.Path)
}

// Validate checks the manifest against the rules of UC-001 (BR-001 to BR-004 and BR-007) and returns an *Error that
// names every problem with its position, or nil.
func (m *Manifest) Validate() error {
	problems := append([]Problem(nil), m.parseProblems...)
	if m.shapeBroken {
		return &Error{Problems: problems}
	}
	add := func(pos Position, where, format string, args ...any) {
		problems = append(problems, Problem{File: m.File, Pos: pos, Where: where, Reason: fmt.Sprintf(format, args...)})
	}
	anchor := func(key string) Position {
		if pos, ok := m.groupPos[key]; ok {
			return pos
		}
		return Position{1, 1}
	}

	m.validateDist(add, anchor)

	entries := m.Entries()
	if m.Runtime != nil {
		entries = append([]GroupEntry{{Group: "runtime", Entry: *m.Runtime}}, entries...)
	}
	if m.Cmd != nil {
		entries = append([]GroupEntry{{Group: "cmd", Entry: *m.Cmd}}, entries...)
	}
	m.validateEntries(entries, add)

	// BR-001 and BR-002: what a distribution contains.
	if len(m.Entries()) == 0 {
		add(anchor("dist"), "", "no component is listed (BR-001: a manifest lists at least one component)")
	}
	if len(m.SyncProviders) == 0 && !m.brokenGroups[string(SyncProviders)] {
		add(anchor(string(SyncProviders)), "", "no sync provider is selected (BR-002: at least one is needed)")
	}
	if len(m.ConfigurationFormats)+len(m.Engines) == 0 && !m.brokenGroups[string(ConfigurationFormats)] && !m.brokenGroups[string(Engines)] {
		add(anchor(string(ConfigurationFormats)), "", "no configuration format is selected (BR-002: at least one is needed, in configuration_formats or as an engine)")
	}
	if len(m.ServiceEndpoints) == 0 && !m.brokenGroups[string(ServiceEndpoints)] {
		add(anchor(string(ServiceEndpoints)), "", "no service endpoint is selected (BR-002: at least one is needed)")
	}

	m.validateReplaces(add)

	if len(problems) == 0 {
		return nil
	}
	sortProblems(problems)
	return &Error{Problems: problems}
}

type adder func(pos Position, where, format string, args ...any)

func (m *Manifest) validateDist(add adder, anchor func(string) Position) {
	pos := m.Dist.Pos
	if pos == (Position{}) {
		add(anchor("dist"), "", "dist is mandatory: it names the distribution, its version and the output location (BR-001)")
		return
	}
	at := func(field string) Position { return m.Dist.fields.at(field, pos) }
	if m.Dist.Name == "" {
		add(pos, "dist", "name is mandatory (BR-001)")
	} else if !distName.MatchString(m.Dist.Name) {
		add(at("name"), "dist", "name %q may hold letters, digits, dots, hyphens and underscores, and starts with a letter or digit", m.Dist.Name)
	}
	if m.Dist.Version == "" {
		add(pos, "dist", "version is mandatory (BR-001)")
	} else if !semver.MatchString(m.Dist.Version) {
		add(at("version"), "dist", "version %q is not a semantic version", m.Dist.Version)
	}
	if m.Dist.OutputPath == "" {
		add(pos, "dist", "output_path is mandatory (BR-001)")
	}
	if m.Dist.Module != "" && !modulePath.MatchString(m.Dist.Module) {
		add(at("module"), "dist", "module %q is not a module path", m.Dist.Module)
	}
	if m.Dist.Binary != "" && !binaryName.MatchString(m.Dist.Binary) {
		add(at("binary"), "dist", "binary %q is not a file name: use letters, digits, dots, hyphens and underscores", m.Dist.Binary)
	}
	if h := m.Dist.ComponentsHint; h != "" && h != "true" && h != "false" {
		add(at("components_hint"), "dist", "components_hint %q must be true or false", h)
	}
}

func (m *Manifest) validateEntries(entries []GroupEntry, add adder) {
	type listed struct {
		pos   Position
		where string
	}
	first := map[string]listed{}
	for _, ge := range entries {
		e, where := ge.Entry, ge.Name()
		if e.invalid {
			continue
		}
		if ge.Group == "runtime" || ge.Group == "cmd" {
			where = string(ge.Group)
		}
		at := func(field string) Position { return e.fields.at(field, e.Pos) }
		switch {
		case e.Module == "":
			add(e.Pos, where, "module is mandatory")
		case !modulePath.MatchString(e.Module):
			add(at("module"), where, "module %q is not a module path", e.Module)
		default:
			if prev, dup := first[e.Module]; dup {
				add(at("module"), where, "the module %s is listed more than once (BR-004); it is already at %s, %s", e.Module, prev.pos, prev.where)
			} else {
				first[e.Module] = listed{at("module"), where}
			}
		}
		switch {
		case e.Version == "":
			add(e.Pos, where, "version is mandatory (BR-003: every component carries an explicit version)")
		case !exactVersion.MatchString(e.Version):
			add(at("version"), where, "version %q is not an exact semantic version or pseudo-version such as v1.2.3 (BR-003: no ranges and no moving references)", e.Version)
		}
		if e.Import != "" && e.Module != "" && e.Import != e.Module && !strings.HasPrefix(e.Import, e.Module+"/") {
			add(at("import"), where, "import %q is not a package of the module %s", e.Import, e.Module)
		}
	}
}

func (m *Manifest) validateReplaces(add adder) {
	seen := map[string]Position{}
	for i, r := range m.Replaces {
		where := fmt.Sprintf("replaces[%d]", i)
		at := func(field string) Position { return r.fields.at(field, r.Pos) }
		switch {
		case r.Module == "":
			add(r.Pos, where, "module is mandatory")
			continue
		case !modulePath.MatchString(r.Module):
			add(at("module"), where, "module %q is not a module path", r.Module)
			continue
		}
		if prev, dup := seen[r.Module]; dup {
			add(at("module"), where, "the module %s is replaced more than once; it is already replaced at %s", r.Module, prev)
		}
		seen[r.Module] = at("module")
		if r.Path == "" {
			add(r.Pos, where, "path is mandatory")
			continue
		}
		if reason := m.checkReplaceTarget(r); reason != "" {
			add(at("path"), where, "%s (BR-007)", reason)
		}
	}
}

// checkReplaceTarget says why a replace does not point to a directory that contains the module, or "".
func (m *Manifest) checkReplaceTarget(r Replace) string {
	dir := m.ReplacePath(r)
	info, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		return fmt.Sprintf("the path %q for %s does not exist (read relative to the manifest's directory)", r.Path, r.Module)
	case err != nil:
		return fmt.Sprintf("the path %q for %s cannot be read: %v", r.Path, r.Module, err)
	case !info.IsDir():
		return fmt.Sprintf("the path %q for %s is not a directory", r.Path, r.Module)
	}
	name, err := moduleOf(filepath.Join(dir, "go.mod"))
	switch {
	case os.IsNotExist(err):
		return fmt.Sprintf("the directory %q for %s holds no go.mod", r.Path, r.Module)
	case err != nil:
		return fmt.Sprintf("the go.mod in %q for %s cannot be read: %v", r.Path, r.Module, err)
	case name != r.Module:
		return fmt.Sprintf("the directory %q holds the module %s, not %s", r.Path, name, r.Module)
	}
	return ""
}

// moduleOf returns the module path a go.mod declares.
func moduleOf(goMod string) (string, error) {
	f, err := os.Open(goMod)
	if err != nil {
		return "", err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if rest, ok := strings.CutPrefix(line, "module"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			return strings.Trim(strings.TrimSpace(rest), `"`+"`"), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no module directive")
}
