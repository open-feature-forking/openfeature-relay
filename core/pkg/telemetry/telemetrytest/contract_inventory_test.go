package telemetrytest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry/telemetrytest"
	"github.com/stretchr/testify/require"
)

// The contract inventory pins what the runner checks: which cases run, in which order, and what each one expects, for a
// provider with a backend and for the noop provider, which has none. A case that is dropped, renamed or loosened
// changes the golden file, which a commit may change only on purpose. UPDATE_GOLDEN=1 rewrites it.

func inventoryOf(title string, r telemetrytest.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", title)
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

	f, in := setup(bugNone)
	withBackend := telemetrytest.Check(t, f, in)
	require.Empty(t, withBackend.FailedCases())
	got.WriteString(inventoryOf("provider with a backend", withBackend))

	noop := telemetrytest.Check(t, telemetry.NoopFactory(), telemetrytest.Inputs{NoBackend: true, NoConfig: true})
	require.Empty(t, noop.FailedCases())
	got.WriteString(inventoryOf("provider without a backend or configuration", noop))

	golden := filepath.Join("testdata", "contract_inventory.golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(golden, []byte(got.String()), 0o644))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err, "run with UPDATE_GOLDEN=1 to create the golden file")
	require.Equal(t, strings.ReplaceAll(string(want), "\r\n", "\n"), got.String())
}
