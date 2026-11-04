package upgrade

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// issueItems returns the items of testdata/issue-78.txt.
func issueItems(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("testdata/issue-78.txt")
	require.NoError(t, err)
	return Issue78Items(string(data))
}

// check returns what is wrong with the entries against the items of the issue: the list is empty when each item of #78
// has exactly one entry, each entry of kind #78 names an item, every entry says how it is shown or pinned, and a known
// break names its owner.
func check(a *Accepted, items []string) []string {
	var problems []string
	matched := make([]int, len(items))
	for _, e := range a.Entries {
		switch e.Kind {
		case KindIssue78:
			found := false
			for i, item := range items {
				if strings.Contains(item, e.Item) {
					matched[i]++
					found = true
				}
			}
			if !found {
				problems = append(problems, "the entry names no item of #78: "+e.Item)
			}
			for _, o := range e.Observations {
				if _, ok := scenarios[o.Scenario]; !ok {
					problems = append(problems, "the observation names no scenario: "+o.Scenario+"/"+o.Key)
				}
				if o.Key == "" || strings.Contains(o.Key, "*") || strings.Contains(o.Scenario, "*") {
					problems = append(problems, "the observation is a wildcard: "+o.Scenario+"/"+o.Key)
				}
				if o.Baseline == o.Candidate {
					problems = append(problems, "the observation accepts no difference: "+o.Scenario+"/"+o.Key)
				}
			}
			if len(e.Observations) == 0 && len(e.CoveredBy) == 0 {
				problems = append(problems, "the entry is neither shown by a scenario nor pinned by a test: "+e.Item)
			}
		case KindKnownBreak:
			if e.Owner == "" {
				problems = append(problems, "the known break has no owner: "+e.Item)
			}
		default:
			problems = append(problems, "unknown kind "+e.Kind+": "+e.Item)
		}
	}
	for i, item := range items {
		if matched[i] != 1 {
			problems = append(problems, "the item of #78 has "+string(rune('0'+matched[i]))+" entries: "+item)
		}
	}
	return problems
}

// TestAcceptedMatchesIssue pins that accepted.yaml and issue 78 list the same items: an item without an entry, or an
// entry without an item, fails.
func TestAcceptedMatchesIssue(t *testing.T) {
	a, err := LoadAccepted("accepted.yaml")
	require.NoError(t, err)
	items := issueItems(t)
	require.Len(t, items, 17, "the items of #78")
	require.Empty(t, check(a, items))

	t.Run("an entry without an item fails", func(t *testing.T) {
		orphan := &Accepted{Entries: append(append([]Entry{}, a.Entries...),
			Entry{Kind: KindIssue78, Item: "A change nobody approved", CoveredBy: []string{"nothing"}})}
		require.Contains(t, strings.Join(check(orphan, items), "\n"), "names no item of #78: A change nobody approved")
	})
	t.Run("an item without an entry fails", func(t *testing.T) {
		fewer := &Accepted{Entries: a.Entries[1:]}
		require.Contains(t, strings.Join(check(fewer, items), "\n"), "has 0 entries: A failed first fetch")
	})
	t.Run("an observation that is a wildcard, or names no scenario, fails", func(t *testing.T) {
		wild := &Accepted{Entries: append(append([]Entry{}, a.Entries...), Entry{Kind: KindIssue78, Item: "A failed first fetch", Observations: []AcceptedDifference{
			{Scenario: "sigterm", Key: "*", Baseline: "a", Candidate: "b"},
			{Scenario: "nowhere", Key: "exit-code", Baseline: "a", Candidate: "b"},
		}})}
		problems := strings.Join(check(wild, items), "\n")
		require.Contains(t, problems, "the observation is a wildcard: sigterm/*")
		require.Contains(t, problems, "the observation names no scenario: nowhere/exit-code")
	})
	t.Run("a difference with other values is not accepted", func(t *testing.T) {
		_, ok := a.Allows(Difference{"unreachable-source", "exit-code", "1", "2"})
		require.False(t, ok)
		_, ok = a.Allows(Difference{"unreachable-source", "exit-code", "1", "<not observed>"})
		require.True(t, ok)
		require.NotEmpty(t, a.Unfired(), "the other observations did not fire")
	})
	t.Run("a known break without an owner fails", func(t *testing.T) {
		ownerless := &Accepted{Entries: append(append([]Entry{}, a.Entries...), Entry{Kind: KindKnownBreak, Item: "go get"})}
		require.Contains(t, strings.Join(check(ownerless, items), "\n"), "has no owner")
	})
}

// TestSurfaces pins that every surface of UC-009 is either run by a scenario that exists or covered by named tests.
func TestSurfaces(t *testing.T) {
	data, err := os.ReadFile("surfaces.yaml")
	require.NoError(t, err)
	var list struct {
		Surfaces []struct {
			Surface   string   `yaml:"surface"`
			Scenario  string   `yaml:"scenario"`
			CoveredBy []string `yaml:"covered_by"`
		} `yaml:"surfaces"`
	}
	require.NoError(t, yaml.Unmarshal(data, &list))
	require.NotEmpty(t, list.Surfaces)
	for _, s := range list.Surfaces {
		if s.Scenario != "" {
			require.Contains(t, scenarios, s.Scenario, s.Surface)
			continue
		}
		require.NotEmpty(t, s.CoveredBy, "the surface is neither run nor covered: "+s.Surface)
	}
}

// TestGuideCoversAcceptedItems pins that the migration guide has a section for every item of #78 that accepted.yaml
// allows, under the item's own words, and names each known break. CI runs it with `make test`, which a change to
// docs/ alone does not trigger: it guards changes to accepted.yaml and to #78.
func TestGuideCoversAcceptedItems(t *testing.T) {
	data, err := os.ReadFile("../../docs/guides/upgrade-to-flagd-full.md")
	require.NoError(t, err)
	guide := strings.ReplaceAll(string(data), "\r\n", "\n")
	a, err := LoadAccepted("accepted.yaml")
	require.NoError(t, err)
	for _, e := range a.Entries {
		switch e.Kind {
		case KindIssue78:
			require.Contains(t, guide, "\n#### "+e.Item+"\n", "the guide has no section for #78's item")
		case KindKnownBreak:
			require.NotEmpty(t, e.Guide, "the known break names no text of the guide: "+e.Item)
			require.Contains(t, guide, e.Guide, "the guide does not name the known break: "+e.Item)
		}
	}
}
