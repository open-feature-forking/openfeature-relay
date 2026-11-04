package upgrade

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kinds of entries in accepted.yaml.
const (
	// KindIssue78 names an item of #78, the behaviour changes that were approved before the drop-in rule.
	KindIssue78 = "#78"
	// KindKnownBreak names a difference that is not accepted for good: the use case that removes it owns it.
	KindKnownBreak = "known-break"
)

// Entry is one difference between today's flagd and the flagd-full the Builder made that the project lead accepts.
type Entry struct {
	Kind string `yaml:"kind"`
	// Item is text that appears in the item of #78 (kind #78) or names the break (kind known-break).
	Item string `yaml:"item"`
	// Owner is the use case that removes a known break.
	Owner string `yaml:"owner,omitempty"`
	// Observations are the differences of the differential that the entry allows: scenario/key. A key may end in *.
	Observations []string `yaml:"observations,omitempty"`
	// CoveredBy names the tests that pin an item the black-box differential cannot observe.
	CoveredBy []string `yaml:"covered_by,omitempty"`
	Note      string   `yaml:"note,omitempty"`
}

// Accepted is the content of accepted.yaml.
type Accepted struct {
	Entries []Entry `yaml:"accepted"`
}

// LoadAccepted reads accepted.yaml.
func LoadAccepted(path string) (*Accepted, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a Accepted
	if err := yaml.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &a, nil
}

// Allows returns the entry that accepts the difference, if any.
func (a *Accepted) Allows(scenario, key string) (Entry, bool) {
	want := scenario + "/" + key
	for _, e := range a.Entries {
		for _, o := range e.Observations {
			if o == want || (strings.HasSuffix(o, "*") && strings.HasPrefix(want, strings.TrimSuffix(o, "*"))) {
				return e, true
			}
		}
	}
	return Entry{}, false
}

// Issue78Items returns the items of the issue text: the lines that start with a checkbox.
func Issue78Items(issue string) []string {
	var items []string
	for _, line := range strings.Split(issue, "\n") {
		if strings.HasPrefix(line, "- [ ] ") || strings.HasPrefix(line, "- [x] ") {
			items = append(items, line[len("- [ ] "):])
		}
	}
	return items
}
