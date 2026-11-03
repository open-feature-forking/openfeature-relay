package manifest

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type pair struct{ key, value *yaml.Node }

// Parse reads a manifest from data; file names it in errors. Only a document that is not YAML is an error here: the
// faults of its shape (an unknown key, a list where a mapping belongs) are kept and reported by Validate together
// with the rule violations, so that one run names every faulty entry (A1).
func Parse(file string, data []byte) (*Manifest, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, &Error{Problems: []Problem{yamlProblem(file, err)}}
	}
	m := &Manifest{File: file, groupPos: map[string]Position{}, brokenGroups: map[string]bool{}}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		m.add(Position{1, 1}, "", "the manifest is empty")
		m.shapeBroken = true
		return m, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		m.add(positionOf(root), "", "the manifest must be a mapping of dist, components and replaces")
		m.shapeBroken = true
		return m, nil
	}

	for _, kv := range m.uniquePairs(root, "") {
		key := kv.key.Value
		m.groupPos[key] = positionOf(kv.key)
		switch key {
		case "dist":
			m.parseDist(kv.value)
		case "runtime":
			if e, ok := m.parseEntry(kv.value, "runtime"); ok {
				m.Runtime = &e
			}
		case string(SyncProviders):
			m.SyncProviders = m.parseEntries(kv.value, key)
		case string(ConfigurationFormats):
			m.ConfigurationFormats = m.parseEntries(kv.value, key)
		case string(Evaluators):
			m.Evaluators = m.parseEntries(kv.value, key)
		case string(Engines):
			m.Engines = m.parseEntries(kv.value, key)
		case string(ServiceEndpoints):
			m.ServiceEndpoints = m.parseEntries(kv.value, key)
		case string(Telemetry):
			if kv.value.Kind == yaml.SequenceNode {
				m.add(positionOf(kv.value), key, "at most one telemetry provider is selected: give a single mapping, not a list")
				continue
			}
			if isNull(kv.value) {
				continue
			}
			if e, ok := m.parseEntry(kv.value, key); ok {
				m.Telemetry = &e
			}
		case string(Middleware):
			m.Middleware = m.parseEntries(kv.value, key)
		case string(EventSinks):
			m.EventSinks = m.parseEntries(kv.value, key)
		case "replaces":
			m.parseReplaces(kv.value)
		default:
			m.add(positionOf(kv.key), "", fmt.Sprintf("unknown key %q (allowed: dist, runtime, %s, replaces)", key, allGroups()))
		}
	}
	return m, nil
}

func allGroups() string {
	groups := []Group{SyncProviders, ConfigurationFormats, Evaluators, Engines, ServiceEndpoints, Telemetry, Middleware, EventSinks}
	names := make([]string, len(groups))
	for i, g := range groups {
		names[i] = string(g)
	}
	return strings.Join(names, ", ")
}

var yamlLine = regexp.MustCompile(`line (\d+)`)

func yamlProblem(file string, err error) Problem {
	p := Problem{File: file, Pos: Position{1, 1}, Reason: "not valid YAML: " + strings.TrimPrefix(err.Error(), "yaml: ")}
	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) && len(typeErr.Errors) > 0 {
		p.Reason = "not valid YAML: " + typeErr.Errors[0]
	}
	if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
		if n, convErr := strconv.Atoi(m[1]); convErr == nil {
			p.Pos.Line = n
		}
	}
	return p
}

func (m *Manifest) add(pos Position, where, reason string) {
	m.parseProblems = append(m.parseProblems, Problem{File: m.File, Pos: pos, Where: where, Reason: reason})
}

// uniquePairs returns the key-value pairs of a mapping node. yaml.v3 keeps a key that appears twice, so each repeat is
// reported with the position of the first and left out: a manifest is an exact record, and no value silently wins.
func (m *Manifest) uniquePairs(n *yaml.Node, where string) []pair {
	out := make([]pair, 0, len(n.Content)/2)
	first := map[string]Position{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i]
		if pos, dup := first[key.Value]; dup {
			m.add(positionOf(key), where, fmt.Sprintf("the key %q appears more than once; it is first at %s", key.Value, pos))
			continue
		}
		first[key.Value] = positionOf(key)
		out = append(out, pair{key, n.Content[i+1]})
	}
	return out
}

func isNull(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Tag == "!!null" }

// scalar returns the text of a scalar node, or reports the fault.
func (m *Manifest) scalar(n *yaml.Node, where, key string) (string, bool) {
	if n.Kind != yaml.ScalarNode || isNull(n) {
		m.add(positionOf(n), where, fmt.Sprintf("%q must be a text", key))
		return "", false
	}
	return n.Value, true
}

func (m *Manifest) parseDist(n *yaml.Node) {
	m.Dist.Pos = positionOf(n)
	m.Dist.fields = fields{}
	if n.Kind != yaml.MappingNode {
		m.add(positionOf(n), "dist", "dist must be a mapping of name, version and output_path")
		return
	}
	for _, kv := range m.uniquePairs(n, "dist") {
		set := func(dst *string) {
			if v, ok := m.scalar(kv.value, "dist", kv.key.Value); ok {
				*dst = v
				m.Dist.fields[kv.key.Value] = positionOf(kv.value)
			}
		}
		switch kv.key.Value {
		case "name":
			set(&m.Dist.Name)
		case "version":
			set(&m.Dist.Version)
		case "output_path":
			set(&m.Dist.OutputPath)
		case "module":
			set(&m.Dist.Module)
		case "binary":
			set(&m.Dist.Binary)
		default:
			m.add(positionOf(kv.key), "dist", fmt.Sprintf("unknown key %q (allowed: name, version, output_path, module, binary)", kv.key.Value))
		}
	}
}

func (m *Manifest) parseEntries(n *yaml.Node, group string) []Entry {
	if isNull(n) {
		return nil
	}
	if n.Kind != yaml.SequenceNode {
		m.add(positionOf(n), group, "must be a list of {module, version} entries")
		m.brokenGroups[group] = true
		return nil
	}
	var out []Entry
	for i, item := range n.Content {
		if e, ok := m.parseEntry(item, fmt.Sprintf("%s[%d]", group, i)); ok {
			out = append(out, e)
		} else {
			// keep the slot so that indexes in later messages match the manifest
			out = append(out, Entry{Pos: positionOf(item), invalid: true})
		}
	}
	return out
}

func (m *Manifest) parseEntry(n *yaml.Node, where string) (Entry, bool) {
	e := Entry{Pos: positionOf(n), fields: fields{}}
	if n.Kind != yaml.MappingNode {
		m.add(positionOf(n), where, "must be a mapping with a module and a version")
		return e, false
	}
	ok := true
	for _, kv := range m.uniquePairs(n, where) {
		set := func(dst *string) {
			if v, good := m.scalar(kv.value, where, kv.key.Value); good {
				*dst = v
				e.fields[kv.key.Value] = positionOf(kv.value)
			} else {
				ok = false
			}
		}
		switch kv.key.Value {
		case "module":
			set(&e.Module)
		case "version":
			set(&e.Version)
		case "import":
			set(&e.Import)
		default:
			m.add(positionOf(kv.key), where, fmt.Sprintf("unknown key %q (allowed: module, version, import)", kv.key.Value))
		}
	}
	return e, ok
}

func (m *Manifest) parseReplaces(n *yaml.Node) {
	if isNull(n) {
		return
	}
	if n.Kind != yaml.SequenceNode {
		m.add(positionOf(n), "replaces", "must be a list of {module, path} entries")
		return
	}
	for i, item := range n.Content {
		where := fmt.Sprintf("replaces[%d]", i)
		r := Replace{Pos: positionOf(item), fields: fields{}}
		if item.Kind != yaml.MappingNode {
			m.add(positionOf(item), where, "must be a mapping with a module and a path")
			m.Replaces = append(m.Replaces, r)
			continue
		}
		for _, kv := range m.uniquePairs(item, where) {
			set := func(dst *string) {
				if v, ok := m.scalar(kv.value, where, kv.key.Value); ok {
					*dst = v
					r.fields[kv.key.Value] = positionOf(kv.value)
				}
			}
			switch kv.key.Value {
			case "module":
				set(&r.Module)
			case "path":
				set(&r.Path)
			default:
				m.add(positionOf(kv.key), where, fmt.Sprintf("unknown key %q (allowed: module, path)", kv.key.Value))
			}
		}
		m.Replaces = append(m.Replaces, r)
	}
}
