package components

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/stretchr/testify/require"
)

// Every component of flagd-full is in the inventory with the module that holds it. The package is generated from
// distributions/flagd-full/manifest.yaml, so this pins the manifest's contents.
func TestInventoryListsEveryComponent(t *testing.T) {
	components := Components()
	inventory := Inventory()

	types := map[distribution.Group][]string{}
	for _, c := range inventory.Components {
		require.NotEmpty(t, c.Module, "%s %s has a module", c.Group, c.Type)
		types[c.Group] = append(types[c.Group], c.Type)
	}

	var syncTypes, endpointTypes []string
	for _, f := range components.Sync {
		syncTypes = append(syncTypes, f.Type())
	}
	for _, f := range components.Endpoints {
		endpointTypes = append(endpointTypes, f.Type())
	}
	require.Equal(t, syncTypes, types[distribution.GroupSync])
	require.Equal(t, endpointTypes, types[distribution.GroupServiceEndpoint])
	require.Equal(t, []string{"flagd"}, types[distribution.GroupConfigurationFormat])
	require.Equal(t, []string{"jsonlogic"}, types[distribution.GroupEvaluator])
	require.Equal(t, []string{"otel"}, types[distribution.GroupTelemetry])
	require.Equal(t, []string{"flag-evaluation", "flag-evaluation-legacy", "ofrep", "flag-sync", "event-collector"}, endpointTypes)

	// the event sinks of flagd-full are available and inert: no sink is configured until the operator names one
	var sinkTypes []string
	for _, f := range components.EventSinkFactories {
		sinkTypes = append(sinkTypes, f.Type())
	}
	require.Equal(t, []string{"webhook"}, sinkTypes)
	require.Equal(t, sinkTypes, types[distribution.GroupEventSink])
	require.Empty(t, components.EventSinks, "flagd-full configures no event sink itself")
	require.Empty(t, components.Middleware)
}
