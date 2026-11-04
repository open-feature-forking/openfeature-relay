package upgrade

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// issueItems returns the items of testdata/issue-78.md.
func issueItems(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("testdata/issue-78.md")
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
	require.Len(t, items, 14, "the items of #78")
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
	t.Run("a known break without an owner fails", func(t *testing.T) {
		ownerless := &Accepted{Entries: append(append([]Entry{}, a.Entries...), Entry{Kind: KindKnownBreak, Item: "go get"})}
		require.Contains(t, strings.Join(check(ownerless, items), "\n"), "has no owner")
	})
}
