package cmd

import (
	"testing"
	"time"

	otelprovider "github.com/open-feature-forking/openfeature-relay/telemetry/otel"
	"github.com/stretchr/testify/require"
)

// TestStartSelectsOpenTelemetry pins the telemetry of flagd-full: flagd start selects the OpenTelemetry telemetry
// provider, never the noop one, and maps each telemetry flag onto its setting. --otel-reload-interval is not bound
// to its flag, as in flagd before the telemetry provider: only FLAGD_OTEL_RELOAD_INTERVAL sets it, so it is 0 here.
func TestStartSelectsOpenTelemetry(t *testing.T) {
	flags := startCmd.Flags()
	bindFlags(flags)

	defaults := officialComponents()
	require.NotNil(t, defaults.Telemetry, "flagd-full has a telemetry provider")
	require.Equal(t, otelprovider.Type, defaults.Telemetry.Type())
	require.Equal(t, map[string]any{
		otelprovider.SettingMetricsExporter: flags.Lookup(metricsExporter).DefValue,
		otelprovider.SettingCollectorURI:    "",
		otelprovider.SettingCertPath:        "",
		otelprovider.SettingKeyPath:         "",
		otelprovider.SettingCAPath:          "",
		otelprovider.SettingReloadInterval:  time.Duration(0),
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

	set := officialComponents()
	require.Equal(t, otelprovider.Type, set.Telemetry.Type())
	require.Equal(t, map[string]any{
		otelprovider.SettingMetricsExporter: "otel",
		otelprovider.SettingCollectorURI:    "collector:4317",
		otelprovider.SettingCertPath:        "/certs/tls.crt",
		otelprovider.SettingKeyPath:         "/certs/tls.key",
		otelprovider.SettingCAPath:          "/certs/ca.crt",
		otelprovider.SettingReloadInterval:  time.Duration(0),
	}, set.TelemetryConfig)
}
