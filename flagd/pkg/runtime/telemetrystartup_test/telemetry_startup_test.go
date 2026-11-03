//go:build unix

// Package telemetrystartup_test pins how FromConfig treats a telemetry configuration it cannot build. It calls
// FromConfig once, in a test binary of its own, because FromConfig installs process-wide telemetry.
package telemetrystartup_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature/flagd/flagd/pkg/runtime"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestTelemetryStartupCharacterization: with a span exporter and a metric reader that cannot be built, FromConfig
// logs both errors and still builds the runtime. UC-006 BR-008 (Q6) changes this.
func TestTelemetryStartupCharacterization(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "foo")
	t.Setenv("OTEL_METRICS_EXPORTER", "bar")

	file := filepath.Join(t.TempDir(), "flags.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"flags":{}}`), 0o600))

	core, logs := observer.New(zapcore.DebugLevel)
	rt, err := runtime.FromConfig(logger.NewLogger(zap.New(core), false), "test", runtime.Config{
		SyncProviders: []coresync.SourceConfig{{URI: file, Provider: "file"}},
	}, runtime.Official())

	require.NoError(t, err, "startup goes on")
	require.NotNil(t, rt)

	var errs []string
	for _, e := range logs.FilterLevelExact(zapcore.ErrorLevel).All() {
		errs = append(errs, e.Message)
	}
	require.Contains(t, errs, `error building trace provider: failed to create span exporter: unknown exporter or metrics producer`)
	require.Contains(t, errs, `error building metrics recorder: failed to setup metric reader: unknown exporter or metrics producer`)
}
