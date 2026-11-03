package evaluatortestkit

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	"github.com/stretchr/testify/require"
)

// The contract inventory pins what the testkit asks of an evaluator: the scenarios of the testbed's evaluator suite that
// run, and the tags it leaves out. A dropped feature file, a changed pin of the testbed or a new exclusion changes the
// golden file, which a commit may change only on purpose. UPDATE_GOLDEN=1 rewrites it.
func TestContractInventory(t *testing.T) {
	var out bytes.Buffer
	suite := godog.TestSuite{
		Name:                "inventory",
		ScenarioInitializer: func(*godog.ScenarioContext) {},
		Options: &godog.Options{
			Format: "progress",
			Output: &out,
			Paths:  []string{filepath.Join(suiteDir, "gherkin")},
			Tags:   tagExpression(),
		},
	}
	suite.Run()
	match := regexp.MustCompile(`(\d+) scenarios?`).FindStringSubmatch(out.String())
	require.NotNil(t, match, "no scenario count in the dry run's output:\n%s", out.String())

	var got strings.Builder
	fmt.Fprintf(&got, "scenarios that run: %s\n", match[1])
	fmt.Fprintf(&got, "tag expression: %s\n", tagExpression())
	for _, e := range excludedTags {
		fmt.Fprintf(&got, "excluded: %s\n", e.tag)
	}

	golden := filepath.Join("testdata", "contract_inventory.golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(golden, []byte(got.String()), 0o644))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err, "run with UPDATE_GOLDEN=1 to create the golden file")
	require.Equal(t, strings.ReplaceAll(string(want), "\r\n", "\n"), got.String())
}

// A suite that yields no scenario is not a pass: Run would otherwise accept an evaluator that was never asked anything.
func TestASuiteWithoutScenariosAbortsTheRun(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gherkin"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "flags"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "flags", "testkit-flags.json"), []byte(`{"flags":{}}`), 0o600))
	defer func(dirBefore, flagsBefore string) { suiteDir, flagsFile = dirBefore, flagsBefore }(suiteDir, flagsFile)
	suiteDir, flagsFile = dir, filepath.Join(dir, "flags", "testkit-flags.json")

	report := Check(t, flagdeval.Factory{})

	require.True(t, report.Failed())
	require.Empty(t, report.Cases)
	require.Contains(t, report.Aborted, "ran no scenario")
}
