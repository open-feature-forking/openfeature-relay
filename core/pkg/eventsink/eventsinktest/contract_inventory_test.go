package eventsinktest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink/eventsinktest"
	"github.com/stretchr/testify/require"
)

// The contract inventory pins what the runner checks: which cases run, in which order, and what each one expects. A
// case that is dropped, renamed or loosened changes the golden file, which a commit may change only on purpose.
// UPDATE_GOLDEN=1 rewrites it.
func TestContractInventory(t *testing.T) {
	ext := newExternal()
	report := eventsinktest.Check(t, stubFactory{ext: ext}, inputs(ext))
	require.Empty(t, report.Aborted)
	require.Empty(t, report.FailedCases())

	var got strings.Builder
	got.WriteString("event sink\n")
	for i, c := range report.Cases {
		fmt.Fprintf(&got, "  %d. %s\n     expected: %s\n", i+1, c.Name, c.Expected)
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
