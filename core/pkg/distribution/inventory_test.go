package distribution

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInventoryVersionsFromBuildInfo(t *testing.T) {
	inventory := Inventory{Components: []ComponentInfo{
		{Group: GroupSync, Type: "file", Module: "example.com/relay/sync/file"},
		{Group: GroupEvaluator, Type: "flagd", Module: "example.com/relay/evaluator/flagd"},
		{Group: GroupServiceEndpoint, Type: "ofrep", Module: "example.com/relay/service/ofrep"},
		{Group: GroupEventSink, Type: "elsewhere", Module: "example.com/relay/eventsink/absent"},
		{Group: GroupMiddleware, Type: "main", Module: "example.com/dist"},
	}}
	info := &debug.BuildInfo{
		Main: debug.Module{Path: "example.com/dist", Version: "(devel)"},
		Deps: []*debug.Module{
			{Path: "example.com/relay/sync/file", Version: "v0.14.0"},
			{Path: "example.com/relay/evaluator/flagd", Version: "v0.0.0-00010101000000-000000000000", Replace: &debug.Module{Path: "../../evaluator/flagd"}},
			{Path: "example.com/relay/service/ofrep", Version: "v0.14.0", Replace: &debug.Module{Path: "example.com/fork/ofrep", Version: "v0.14.1"}},
		},
	}

	got := inventory.ResolvedFrom(info).Components

	require.Equal(t, "v0.14.0", got[0].Version)
	require.Empty(t, got[0].Replacement)
	require.Equal(t, "dev", got[1].Version, "a module replaced by a local path has no version of its own")
	require.Equal(t, "../../evaluator/flagd", got[1].Replacement)
	require.Equal(t, "v0.14.1", got[2].Version, "a replaced module shows its replacement")
	require.Equal(t, "example.com/fork/ofrep", got[2].Replacement)
	require.Empty(t, got[3].Version, "a module that is not linked has no version")
	require.Equal(t, "(devel)", got[4].Version, "the main module")

	require.Empty(t, inventory.Components[0].Version, "the inventory itself is not changed")
}

func TestResolvedWithoutBuildInformation(t *testing.T) {
	// the test binary has build information, but none of these modules are in it
	got := Inventory{Components: []ComponentInfo{{Group: GroupSync, Type: "x", Module: "example.com/none"}}}.Resolved()
	require.Len(t, got.Components, 1)
	require.Empty(t, got.Components[0].Version)
}

func TestResolvedKeepsTheHost(t *testing.T) {
	got := Inventory{Components: []ComponentInfo{
		{Group: GroupServiceEndpoint, Type: "legacy", Module: "example.com/none", Host: "flag-evaluation"},
		{Group: GroupServiceEndpoint, Type: "flag-evaluation", Module: "example.com/none"},
	}}.ResolvedFrom(&debug.BuildInfo{}).Components
	require.Equal(t, "flag-evaluation", got[0].Host)
	require.Empty(t, got[1].Host)
}
