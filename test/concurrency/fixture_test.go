package concurrency

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

type fixtureEntry struct {
	Gen          int    `json:"gen"`
	Key          string `json:"key"`
	TargetingKey string `json:"targetingKey"`
	Want         Answer `json:"want"`
}

// fixture lists the expected answers k6/check.js is held to: every key of generations 0..12 and the targeting flag for
// every last digit of the targeting key.
func fixture() []fixtureEntry {
	var out []fixtureEntry
	for g := 0; g <= 12; g++ {
		keys := []string{OddOnly}
		for _, set := range Sets {
			keys = append(keys, Keys(set)...)
		}
		keys = append(keys, "a-gen-0", "a-str-10", "c-str-0")
		for _, k := range keys {
			out = append(out, fixtureEntry{g, k, "user-3", Expected(k, g, "user-3")})
		}
		for d := 0; d < 10; d++ {
			tk := fmt.Sprintf("user-%d", d)
			out = append(out, fixtureEntry{g, "b-tgt-0", tk, Expected("b-tgt-0", g, tk)})
		}
	}
	return out
}

// TestGeneratorMatchesFixture: generations.go and check.js agree on the expected answers (the self-test checks check.js
// against the same file). UPDATE_FIXTURE=1 rewrites it.
func TestGeneratorMatchesFixture(t *testing.T) {
	want, err := json.Marshal(fixture())
	require.NoError(t, err)
	if os.Getenv("UPDATE_FIXTURE") != "" {
		require.NoError(t, os.WriteFile("testdata/fixture.json", want, 0o644))
	}
	got, err := os.ReadFile("testdata/fixture.json")
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got), "run UPDATE_FIXTURE=1 go test to rewrite testdata/fixture.json")
}

// TestDocumentHoldsTheGeneration: the flag file carries the generation the conductor and k6 read back from it.
func TestDocumentHoldsTheGeneration(t *testing.T) {
	for _, g := range []int{0, 1, 2, 199} {
		got, err := Generation(Document(g))
		require.NoError(t, err)
		require.Equal(t, g, got)
		var doc struct {
			Flags map[string]any `json:"flags"`
		}
		require.NoError(t, json.Unmarshal(Document(g), &doc))
		want := 2 * (5*PerKind + 1)
		if g%2 == 1 {
			want++
		}
		require.Len(t, doc.Flags, want)
	}
}
