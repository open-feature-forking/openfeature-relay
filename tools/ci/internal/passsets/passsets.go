// Package passsets compares the scenarios that two runs of go test -json passed (UC-022 Q14). It is the port of
// .github/scripts/compare-pass-sets.py.
//
// It fails when the two sets differ, or when a run passed nothing. A scenario is a test or subtest name; the packages
// are not part of it.
package passsets

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/ghactions"
	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/textio"
)

// usage is the docstring of the Python script with the command renamed.
const usage = `Compares the scenarios that two runs of go test -json passed (UC-022 Q14).

usage: relay-ci compare-pass-sets <launchpad.json> <relay.json>

Fails when the two sets differ, or when a run passed nothing. A scenario is a test or subtest name; the packages are not
part of it.

`

// Passed returns the names of the tests a go test -json output passed, and the names it failed. A line that is not a
// JSON object is skipped. Field names match exactly, as in Python.
func Passed(text string) (ok, failed map[string]bool) {
	ok, failed = map[string]bool{}, map[string]bool{}
	for _, l := range textio.SplitLines(text) {
		var event map[string]json.RawMessage
		if json.Unmarshal([]byte(l), &event) != nil {
			continue
		}
		var name, action string
		if json.Unmarshal(event["Test"], &name) != nil || name == "" {
			continue
		}
		_ = json.Unmarshal(event["Action"], &action)
		switch action {
		case "pass":
			ok[name] = true
		case "fail":
			failed[name] = true
		}
	}
	return ok, failed
}

// sortedDifference returns the keys of a that b lacks, sorted.
func sortedDifference(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Compare returns the summary lines and the problems of two outputs of go test -json.
func Compare(launchpadText, relayText string) (lines, problems []string) {
	launchpadOK, launchpadFailed := Passed(launchpadText)
	relayOK, relayFailed := Passed(relayText)
	lines = []string{
		"### Launchpad and relay testbed: scenarios passed",
		"",
		fmt.Sprintf("- launchpad: %d passed, %d failed", len(launchpadOK), len(launchpadFailed)),
		fmt.Sprintf("- relay testbed: %d passed, %d failed", len(relayOK), len(relayFailed)),
	}
	if len(launchpadOK) == 0 {
		problems = append(problems, "the launchpad run passed no scenario")
	}
	if len(relayOK) == 0 {
		problems = append(problems, "the relay testbed run passed no scenario")
	}
	for _, name := range sortedDifference(launchpadOK, relayOK) {
		problems = append(problems, "passes on the launchpad and not on the relay testbed: "+name)
	}
	for _, name := range sortedDifference(relayOK, launchpadOK) {
		problems = append(problems, "passes on the relay testbed and not on the launchpad: "+name)
	}
	if len(problems) > 0 {
		lines = append(lines, "", "Differences:")
		for _, p := range problems {
			lines = append(lines, "- "+p)
		}
	}
	return lines, problems
}

// Run is the relay-ci compare-pass-sets subcommand: args are <launchpad.json> <relay.json>. It returns the exit code:
// 0 when the sets are equal, 1 when they differ, a run passed nothing or a file cannot be read, 2 for bad arguments.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		_, _ = io.WriteString(stdout, usage)
		return 2
	}
	launchpad, err := textio.ReadText(args[0])
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	relay, err := textio.ReadText(args[1])
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	lines, problems := Compare(launchpad, relay)
	text := strings.Join(lines, "\n")
	if err := ghactions.AppendSummary(text + "\n"); err != nil {
		_, _ = fmt.Fprintf(stderr, "cannot write the step summary: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, text)
	for _, p := range problems {
		_, _ = fmt.Fprintf(stdout, "::error::%s\n", p)
	}
	if len(problems) > 0 {
		return 1
	}
	return 0
}
