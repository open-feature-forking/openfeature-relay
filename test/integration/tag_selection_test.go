package integration_test

import (
	"os"
	"strings"
	"testing"
)

// TestTagSelectionMatchesBranchPoint pins which Gherkin scenarios run: the RPC expression on the first line of
// testdata/tag-selection.golden, the in-process expression on the second, as in upstream b23f82ec. Widening or
// narrowing a tag changes what the suite proves, so a change is accepted by updating the golden with a reason.
func TestTagSelectionMatchesBranchPoint(t *testing.T) {
	raw, err := os.ReadFile("testdata/tag-selection.golden")
	if err != nil {
		t.Fatal(err)
	}
	golden := strings.Split(strings.TrimRight(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n"), "\n")
	if len(golden) != 2 {
		t.Fatalf("golden has %d lines, want 2 (RPC, in-process)", len(golden))
	}
	for i, c := range []struct{ name, got string }{{"RPC", rpcTags}, {"in-process", inProcessTags}} {
		if c.got != golden[i] {
			t.Errorf("%s tag expression changed\n got: %s\nwant: %s", c.name, c.got, golden[i])
		}
	}
}
