package middlewaretest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware/middlewaretest"
	"github.com/stretchr/testify/require"
)

// The contract inventory pins which cases the runner makes of a middleware, in which order, and what each one expects, for
// a middleware that answers and takes configuration and for one that does neither (the declared modes NeverAnswers and
// NoConfig). A case that is dropped, renamed or loosened changes the golden file, which a commit may change only on
// purpose. UPDATE_GOLDEN=1 rewrites it.
func TestContractInventory(t *testing.T) {
	runs := []struct {
		title   string
		factory middleware.Factory
		inputs  middlewaretest.Inputs
	}{
		{"middleware that answers and takes configuration", stubFactory{typ: "stub"}, inputs()},
		{"middleware that never answers and takes no configuration", passThrough{}, middlewaretest.Inputs{
			Passed: request(""), NeverAnswers: true, NoConfig: true,
		}},
	}

	var got strings.Builder
	for _, r := range runs {
		report := middlewaretest.Check(r.factory, r.inputs)
		require.Empty(t, report.Aborted)
		require.Empty(t, report.FailedCases())

		fmt.Fprintf(&got, "%s\n", r.title)
		for i, c := range report.Cases {
			fmt.Fprintf(&got, "  %d. %s\n     expected: %s\n", i+1, c.Name, c.Expected)
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
