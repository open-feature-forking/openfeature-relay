package formattest

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const inventoryHelper = "FORMATTEST_INVENTORY_HELPER"

// TestInventoryHelper runs the contract on the conforming format, as an author's test would. It runs only as the
// helper process of TestContractInventory, which reads the names of its subtests.
func TestInventoryHelper(t *testing.T) {
	mode := os.Getenv(inventoryHelper)
	if mode == "" {
		t.Skip("runs only as a helper process")
	}
	samples := kvSamples
	if mode == "without optional samples" {
		samples.Incremental, samples.Dropped = nil, nil
		samples.Without = []Feature{FeatureIncremental, FeatureDropped}
	}
	Run(t, kvFactory{}, samples)
}

// The contract inventory pins which checks the runner makes of a format, in which order, and that each passes on a
// conforming format. A check that is dropped or renamed changes the golden file, which a commit may change only on
// purpose. UPDATE_GOLDEN=1 rewrites it.
func TestContractInventory(t *testing.T) {
	result := regexp.MustCompile(`(?m)^\s+--- (PASS|FAIL|SKIP): TestInventoryHelper/(\S+)`)

	var got strings.Builder
	// without Incremental and Dropped the runner still passes those two checks, without exercising them
	for _, mode := range []string{"all samples", "without optional samples"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestInventoryHelper$", "-test.v")
		cmd.Env = append(os.Environ(), inventoryHelper+"="+mode)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))

		got.WriteString("configuration format, " + mode + "\n")
		for i, m := range result.FindAllStringSubmatch(string(out), -1) {
			got.WriteString("  " + strconv.Itoa(i+1) + ". " + strings.ReplaceAll(m[2], "_", " ") + ": " + m[1] + "\n")
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
