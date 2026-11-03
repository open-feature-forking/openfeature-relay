package evaluatortest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The contract inventory pins which cases the runner makes of an evaluator, in which order, and that each passes on a
// conforming evaluator, with every optional sample and with none (the evaluatortest runner listed no case before this
// inventory). A case that is dropped or renamed changes the golden file, which a commit may change only on purpose.
// UPDATE_GOLDEN=1 rewrites it.
func TestContractInventory(t *testing.T) {
	var got strings.Builder
	for _, mode := range []string{"all samples", "without optional samples"} {
		e := &reference{}
		samples := samplesFor(e)
		if mode != "all samples" {
			samples.Disabled, samples.AbsentDefault = "", ""
			samples.Attribute, samples.Paired, samples.Shared = nil, nil, nil
			samples.Without = []Feature{FeatureDisabled, FeatureAbsentDefault, FeatureAttribute, FeaturePaired, FeatureShared}
		}
		report := Check(t, e, samples)
		require.Empty(t, report.Aborted)
		require.Empty(t, report.FailedCases())

		fmt.Fprintf(&got, "evaluator, %s\n", mode)
		for i, c := range report.Cases {
			fmt.Fprintf(&got, "  %d. %s", i+1, c.Name)
			if c.NotApplicable != "" {
				fmt.Fprintf(&got, " (not applicable: %s)", c.NotApplicable)
			}
			got.WriteString("\n")
		}
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
