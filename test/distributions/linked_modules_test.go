package distributions

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// modulesOf returns the main module and the dependencies of a binary, as go version -m lists them.
func modulesOf(t *testing.T, binaryPath string) map[string]bool {
	t.Helper()
	out, err := exec.Command("go", "version", "-m", binaryPath).Output()
	require.NoError(t, err)
	modules := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && (fields[0] == "mod" || fields[0] == "dep") {
			modules[fields[1]] = true
		}
	}
	return modules
}

// TestFlagdFullLinkedModules pins the modules the flagd-full that the Builder makes links, as go version -m lists them
// (what a scanner of a release reads). They are the modules testdata/flagd-full-modules.txt lists for the source tree,
// so the two cannot drift apart. A change of the list goes in the commit that causes it, with an accepted.yaml entry.
func TestFlagdFullLinkedModules(t *testing.T) {
	path := filepath.Join(root(t), "distributions", "flagd-full", "manifest.yaml")
	b := build(t, path)
	require.NoError(t, b.err, b.out)
	linkedModules := modulesOf(t, binary(t, path))

	data, err := os.ReadFile(filepath.Join(root(t), "testdata", "flagd-full-modules.txt"))
	require.NoError(t, err)
	listed := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if line != "" {
			listed[line] = true
		}
	}

	var added, removed []string
	for module := range linkedModules {
		if !listed[module] {
			added = append(added, module)
		}
	}
	for module := range listed {
		if !linkedModules[module] {
			removed = append(removed, module)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	require.Empty(t, added, "modules the binary links that testdata/flagd-full-modules.txt does not list")
	require.Empty(t, removed, "modules listed in testdata/flagd-full-modules.txt that the binary does not link")
}
