package telemetrytest_test

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry/telemetrytest"
)

// extensionProviderFactory is the Extension Author's own telemetry provider factory.
var extensionProviderFactory telemetry.Factory

func ExampleRun() {
	_ = func(t *testing.T) {
		telemetrytest.Run(t, extensionProviderFactory, telemetrytest.Inputs{
			Config:        map[string]any{"endpoint": "http://backend.example"},
			InvalidConfig: map[string]any{"endpoint": 42},
			FaultyValue:   "endpoint",
			// stop the backend from accepting data until release is called
			StallBackend: func(testing.TB) (release func()) { return func() {} },
			// what the backend received
			Observed: func(testing.TB) telemetrytest.Observation { return telemetrytest.Observation{} },
		})
	}
}
