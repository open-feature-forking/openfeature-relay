//go:build unix

// Package telemetrystartup_test pins how FromConfig treats a telemetry configuration it cannot build. Every
// FromConfig call of this package fails before the provider is complete; none registers the Prometheus exporter.
package telemetrystartup_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	otelprovider "github.com/open-feature-forking/openfeature-relay/telemetry/otel"
	"github.com/open-feature/flagd/flagd/pkg/runtime"
	"github.com/stretchr/testify/require"
)

func startup(t *testing.T, config map[string]any) (*runtime.Runtime, error) {
	t.Helper()

	file := filepath.Join(t.TempDir(), "flags.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"flags":{}}`), 0o600))
	components := runtime.Official()
	components.Telemetry = otelprovider.NewFactory()
	components.TelemetryConfig = config
	return runtime.FromConfig(logger.NewLogger(nil, false), "test", runtime.Config{
		SyncProviders: []coresync.SourceConfig{{URI: file, Provider: "file"}},
	}, components)
}

// TestTelemetryStartupCharacterization: with a span exporter or a metric reader that cannot be built, FromConfig
// fails and names the telemetry provider and the faulty value. It used to log both errors and build the runtime
// without telemetry (UC-006 BR-008, Q6).
func TestTelemetryStartupCharacterization(t *testing.T) {
	t.Run("span exporter", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "foo")

		rt, err := startup(t, nil)

		require.Nil(t, rt)
		require.ErrorContains(t, err, `telemetry provider "otel"`)
		require.ErrorContains(t, err, `OTEL_TRACES_EXPORTER="foo"`)
		require.ErrorContains(t, err, "unknown exporter or metrics producer")
	})

	t.Run("metric reader", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "none")
		t.Setenv("OTEL_METRICS_EXPORTER", "bar")

		rt, err := startup(t, nil)

		require.Nil(t, rt)
		require.ErrorContains(t, err, `telemetry provider "otel"`)
		require.ErrorContains(t, err, "failed to setup metric reader")
		require.ErrorContains(t, err, "unknown exporter or metrics producer")
	})
}

// TestInvalidTelemetryConfigStops: a setting the telemetry provider does not accept stops startup, and the error
// names the provider, the setting and the value.
func TestInvalidTelemetryConfigStops(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")

	for name, tt := range map[string]struct {
		config map[string]any
		want   string
	}{
		"unknown setting":  {map[string]any{"collector": "localhost:4317"}, `unknown setting "collector"`},
		"wrong value type": {map[string]any{"collectorURI": 3}, `setting "collectorURI": 3 is not a string`},
		"bad duration":     {map[string]any{"reloadInterval": "soon"}, `setting "reloadInterval": "soon" is not a duration`},
	} {
		t.Run(name, func(t *testing.T) {
			rt, err := startup(t, tt.config)

			require.Nil(t, rt)
			require.ErrorContains(t, err, `error creating the telemetry provider "otel"`)
			require.ErrorContains(t, err, tt.want)
		})
	}
}
