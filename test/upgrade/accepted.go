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
	// Observations are the differences of the differential that the entry allows, each with the values both flagds are
	// expected to show.
	Observations []AcceptedDifference `yaml:"observations,omitempty"`
	// CoveredBy names the tests that pin an item the black-box differential cannot observe.
	CoveredBy []string `yaml:"covered_by,omitempty"`
	// Guide is text of the migration guide that names a known break.
	Guide string `yaml:"guide,omitempty"`
	Note  string `yaml:"note,omitempty"`
}

// AcceptedDifference is one difference an entry accepts: the observation of a scenario, and what each flagd shows. A
// difference with other values is not accepted. An absent observation is "<not observed>".
type AcceptedDifference struct {
	Scenario  string `yaml:"scenario"`
	Key       string `yaml:"key"`
	Baseline  string `yaml:"baseline"`
	Candidate string `yaml:"candidate"`
}

// Accepted is the content of accepted.yaml.
type Accepted struct {
	Entries []Entry `yaml:"accepted"`
	fired   map[AcceptedDifference]bool
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

// Allows returns the entry that accepts the difference, if any. It matches the scenario, the key and both values
// exactly, and notes that the observation fired.
func (a *Accepted) Allows(d Difference) (Entry, bool) {
	got := AcceptedDifference{d.Scenario, d.Key, d.Baseline, d.Candidate}
	for _, e := range a.Entries {
		for _, o := range e.Observations {
			if o == got {
				if a.fired == nil {
					a.fired = map[AcceptedDifference]bool{}
				}
				a.fired[o] = true
				return e, true
			}
		}
	}
	return Entry{}, false
}

// Unfired returns the observations of the entries that no difference matched.
func (a *Accepted) Unfired() []AcceptedDifference {
	var out []AcceptedDifference
	for _, e := range a.Entries {
		for _, o := range e.Observations {
			if !a.fired[o] {
				out = append(out, o)
			}
		}
	}
	return out
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
