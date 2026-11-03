package servicetest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/stretchr/testify/require"
)

// The contract inventory pins what the runner checks of each kind of endpoint: which cases run and in which order.
// A case that is dropped or renamed changes the golden file, which a commit may change only on purpose.
// UPDATE_GOLDEN=1 rewrites it.
func TestContractInventory(t *testing.T) {
	kinds := []struct {
		name    string
		factory stub
		inputs  Inputs
	}{
		{"resolver", stub{typeName: "resolver", needs: service.Needs{Router: true}, features: resolverFeatures}, inputsFor(resolverFeatures, true)},
		{"vendor evaluator", stub{typeName: "vendor", needs: service.Needs{Format: "flagd"}, features: resolverFeatures}, inputsFor(resolverFeatures, false)},
		{"config server", stub{typeName: "config-server", features: serverFeatures}, inputsFor(serverFeatures, false)},
		{"collector", stub{typeName: "collector", features: collectFeatures}, inputsFor(collectFeatures, false)},
		{"silent", stub{typeName: "silent"}, inputsFor(features{}, false)},
	}
	var got strings.Builder
	for _, k := range kinds {
		results := Check(k.factory, k.inputs)
		require.Empty(t, failed(results), "%+v", results)
		fmt.Fprintf(&got, "%s\n", k.name)
		for i, name := range names(results) {
			fmt.Fprintf(&got, "  %d. %s\n", i+1, name)
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
