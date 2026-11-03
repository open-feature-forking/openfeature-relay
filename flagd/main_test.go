package main

import (
	"testing"

	otelprovider "github.com/open-feature-forking/openfeature-relay/telemetry/otel"
	"github.com/stretchr/testify/require"
)

// TestFlagdFullSelectsOpenTelemetry pins the telemetry of flagd-full: its components carry the OpenTelemetry
// telemetry provider, never the noop one.
func TestFlagdFullSelectsOpenTelemetry(t *testing.T) {
	components := officialComponents()
	require.NotNil(t, components.Telemetry, "flagd-full has a telemetry provider")
	require.Equal(t, otelprovider.Type, components.Telemetry.Type())
}
