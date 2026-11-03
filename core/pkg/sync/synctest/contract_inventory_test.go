package synctest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
	"github.com/stretchr/testify/require"
)

// The contract inventory pins what each runner checks: which cases run, in which order, and what each one expects. A
// case that is dropped, renamed or loosened changes the golden file, which a commit may change only on purpose.
// UPDATE_GOLDEN=1 rewrites it.

func inventoryOf(component string, r synctest.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", component)
	if r.Aborted != "" {
		fmt.Fprintf(&b, "  aborted: %s\n", r.Aborted)
	}
	for i, c := range r.Cases {
		fmt.Fprintf(&b, "  %d. %s\n     expected: %s\n", i+1, c.Name, c.Expected)
	}
	return b.String()
}

func TestContractInventory(t *testing.T) {
	var got strings.Builder

	fetcher, fin := fetcherSetup(t, fetcherOK)
	fetcherReport := synctest.CheckFetcher(t, fetcher, fin)
	require.Empty(t, fetcherReport.FailedCases())
	got.WriteString(inventoryOf("fetcher", fetcherReport))

	trigger, tin := triggerSetup(t, triggerOK)
	triggerReport := synctest.CheckTrigger(t, trigger, tin)
	require.Empty(t, triggerReport.FailedCases())
	got.WriteString(inventoryOf("fetch trigger", triggerReport))

	stream, sin := streamSetup(t, streamOK)
	streamReport := synctest.CheckStreaming(t, stream, sin)
	require.Empty(t, streamReport.FailedCases())
	got.WriteString(inventoryOf("streaming sync provider", streamReport))

	golden := filepath.Join("testdata", "contract_inventory.golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(golden, []byte(got.String()), 0o644))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err, "run with UPDATE_GOLDEN=1 to create the golden file")
	require.Equal(t, strings.ReplaceAll(string(want), "\r\n", "\n"), got.String())
}
