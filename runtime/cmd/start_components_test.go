package cmd

import (
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/stretchr/testify/require"
)

// TestStartMapsTelemetryFlags pins how flagd start maps each telemetry flag onto a setting of the telemetry provider.
// --otel-reload-interval is not bound to its flag, as in flagd before the telemetry provider: only
// FLAGD_OTEL_RELOAD_INTERVAL sets it, so it is 0 here.
func TestStartMapsTelemetryFlags(t *testing.T) {
	flags := startCmd.Flags()
	bindFlags(flags)

	defaults := withTelemetryConfig(distribution.Components{})
	require.Equal(t, map[string]any{
		"metricsExporter": flags.Lookup(metricsExporter).DefValue,
		"collectorURI":    "",
		"certPath":        "",
		"keyPath":         "",
		"caPath":          "",
		"reloadInterval":  time.Duration(0),
	}, defaults.TelemetryConfig)

	for name, value := range map[string]string{
		metricsExporter:            "otel",
		otelCollectorURI:           "collector:4317",
		otelCertPathFlagName:       "/certs/tls.crt",
		otelKeyPathFlagName:        "/certs/tls.key",
		otelCAPathFlagName:         "/certs/ca.crt",
		otelReloadIntervalFlagName: "15m",
	} {
		require.NoError(t, flags.Set(name, value))
	}
	t.Cleanup(func() {
		for _, name := range []string{metricsExporter, otelCollectorURI, otelCertPathFlagName, otelKeyPathFlagName,
			otelCAPathFlagName, otelReloadIntervalFlagName} {
			f := flags.Lookup(name)
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		}
	})

	set := withTelemetryConfig(distribution.Components{})
	require.Equal(t, map[string]any{
		"metricsExporter": "otel",
		"collectorURI":    "collector:4317",
		"certPath":        "/certs/tls.crt",
		"keyPath":         "/certs/tls.key",
		"caPath":          "/certs/ca.crt",
		"reloadInterval":  time.Duration(0),
	}, set.TelemetryConfig)
}
