package main

import (
	"testing"

	otelprovider "github.com/open-feature-forking/openfeature-relay/telemetry/otel"
	"github.com/open-feature/flagd/flagd/components"
	"github.com/stretchr/testify/require"
)

// TestFlagdFullSelectsOpenTelemetry pins the telemetry of flagd-full: its components carry the OpenTelemetry
// telemetry provider, never the noop one.
func TestFlagdFullSelectsOpenTelemetry(t *testing.T) {
	selected := components.Components()
	require.NotNil(t, selected.Telemetry, "flagd-full has a telemetry provider")
	require.Equal(t, otelprovider.Type, selected.Telemetry.Type())
}
