// Package manifest reads and validates the manifest of the Builder (UC-001): the distribution it names and the
// components, grouped by component type, that the distribution compiles in. Every error carries the line and column
// of the faulty entry in the manifest (A1).
package manifest

import (
	"bytes"
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// Position is a place in the manifest, counted from 1.
type Position struct {
	Line, Column int
}

func (p Position) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Column) }

func positionOf(n *yaml.Node) Position { return Position{Line: n.Line, Column: n.Column} }

// Dist names the distribution.
type Dist struct {
	Name       string
	Version    string
	OutputPath string
	// Module is the module path of the generated source; it defaults to Name.
	Module string
	// Binary is the name of the binary; it defaults to "flagd" (the drop-in rule).
	Binary string
	Pos    Position
}

// Entry is one component module of the manifest.
type Entry struct {
	Module  string
	Version string
	// Import is the package that holds the component's factory; it defaults to the module root.
	Import string
	Pos    Position

	// invalid marks an entry whose shape Parse reported already, so that Validate does not repeat itself.
	invalid bool
}

// Replace maps a module path to a local directory, as a replace directive does.
type Replace struct {
	Module string
	// Path is written as in the manifest and read relative to the manifest's directory.
	Path string
	Pos  Position
}

// Group names a group of components in the manifest.
type Group string

// The groups of components, in the order a manifest lists them.
const (
	SyncProviders        Group = "sync_providers"
	ConfigurationFormats Group = "configuration_formats"
	Evaluators           Group = "evaluators"
	Engines              Group = "engines"
	ServiceEndpoints     Group = "service_endpoints"
	Telemetry            Group = "telemetry"
	Middleware           Group = "middleware"
	EventSinks           Group = "event_sinks"
)

// Manifest is a parsed manifest. It is not validated: see Validate.
type Manifest struct {
	// File is the path the manifest was read from; it anchors the replaces and names the file in errors.
	File string
	Dist Dist
	// Runtime pins the runtime module; it is optional.
	Runtime *Entry

	SyncProviders        []Entry
	ConfigurationFormats []Entry
	Evaluators           []Entry
	Engines              []Entry
	ServiceEndpoints     []Entry
	// Telemetry is the telemetry provider; at most one, and the noop provider when absent.
	Telemetry  *Entry
	Middleware []Entry
	EventSinks []Entry
	Replaces   []Replace

	// groupPos is where each key of the document is, for problems about a group as a whole.
	groupPos map[string]Position
	// shapeBroken is set when the document is not a mapping at all, so that the rules are not checked against nothing.
	shapeBroken bool
	// brokenGroups are the groups whose value Parse could not read.
	brokenGroups map[string]bool
	// parseProblems are what Parse found wrong with the shape of the document.
	parseProblems []Problem
}

// Entries returns every component entry with the group it is listed under, in manifest order. The runtime entry is
// not a component and is left out.
func (m *Manifest) Entries() []GroupEntry {
	var out []GroupEntry
	add := func(g Group, entries ...Entry) {
		for i, e := range entries {
			out = append(out, GroupEntry{Group: g, Index: i, Entry: e})
		}
	}
	add(SyncProviders, m.SyncProviders...)
	add(ConfigurationFormats, m.ConfigurationFormats...)
	add(Evaluators, m.Evaluators...)
	add(Engines, m.Engines...)
	add(ServiceEndpoints, m.ServiceEndpoints...)
	if m.Telemetry != nil {
		add(Telemetry, *m.Telemetry)
	}
	add(Middleware, m.Middleware...)
	add(EventSinks, m.EventSinks...)
	return out
}

// GroupEntry is an entry with the group and index it was listed under.
type GroupEntry struct {
	Group Group
	Index int
	Entry Entry
}

// Name describes where the entry is in the manifest, such as "sync_providers[1]" or "telemetry".
func (g GroupEntry) Name() string {
	if g.Group == Telemetry {
		return string(g.Group)
	}
	return fmt.Sprintf("%s[%d]", g.Group, g.Index)
}

// Problem is one fault of a manifest.
type Problem struct {
	File string
	Pos  Position
	// Where describes the entry, such as "sync_providers[1]"; it can be empty.
	Where  string
	Reason string
}

func (p Problem) String() string {
	where := ""
	if p.Where != "" {
		where = p.Where + ": "
	}
	return fmt.Sprintf("%s:%s: %s%s", p.File, p.Pos, where, p.Reason)
}

// Error is the error of an invalid manifest: every problem, in manifest order.
type Error struct {
	Problems []Problem
}

func (e *Error) Error() string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		lines[i] = p.String()
	}
	return fmt.Sprintf("the manifest has %d problem(s):\n", len(e.Problems)) + joinLines(lines)
}

func joinLines(lines []string) string {
	var b bytes.Buffer
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("  " + l)
	}
	return b.String()
}

func sortProblems(problems []Problem) {
	sort.SliceStable(problems, func(i, j int) bool {
		a, b := problems[i].Pos, problems[j].Pos
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
}

// Load reads and parses the manifest at path, and validates it. A manifest that cannot be read, is not YAML or is
// invalid is an error; an invalid one is an *Error naming every problem.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the manifest: %w", err)
	}
	m, err := Parse(path, data)
	if err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}
