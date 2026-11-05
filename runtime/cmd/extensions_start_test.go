package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/runtime"
	"github.com/open-feature-forking/openfeature-relay/runtime/runtimetest"
	"github.com/stretchr/testify/require"
)

const (
	extensionOutEnv  = "RELAY_EXTENSION_OUT"
	extensionArgsEnv = "RELAY_EXTENSION_ARGS"
)

// TestExtensionStartHelper is not a test: the cases below run the test binary again with it, so that each gets fresh
// cobra and viper state. It executes the root command for flagd-full's components plus an event sink and a middleware
// factory, and writes what `flagd start` hands to the runtime, instead of starting it.
func TestExtensionStartHelper(_ *testing.T) {
	out := os.Getenv(extensionOutEnv)
	if out == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv(extensionArgsEnv)), &args); err != nil {
		panic(err)
	}
	fromConfig = func(_ *logger.Logger, _ string, cfg runtime.Config, c distribution.Components) (*runtime.Runtime, error) {
		line := fmt.Sprintf("needsMetrics=%v eventSinks=%d middleware=%d\n", cfg.EventSinksNeedMetrics, len(c.EventSinks), len(c.Middleware))
		if err := os.WriteFile(out, []byte(line), 0o600); err != nil {
			panic(err)
		}
		os.Exit(0)
		return nil, nil
	}
	c := runtimetest.Components()
	c.EventSinkFactories = []eventsink.Factory{sinkFactory{"webhook"}}
	c.MiddlewareFactories = nil
	rootCmd.SetArgs(args)
	Execute(distribution.Info{Name: "custom", Version: "v9.9.9", Commit: "abc1234", Date: "2026-01-02T03:04:05Z"}, c, distribution.Inventory{})
}

// runExtensionStart runs `flagd start` with the configuration file in an empty home directory. It returns what the
// runtime would have been given, or the standard error of a start that stopped.
func runExtensionStart(t *testing.T, file string) (given string, stopped string) {
	t.Helper()
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, ".agent.yaml"), []byte(file), 0o600))
	rawArgs, err := json.Marshal([]string{"start"})
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), "given.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExtensionStartHelper$")
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), extensionOutEnv + "=" + out, extensionArgsEnv + "=" + string(rawArgs)}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", stderr.String()
	}
	data, err := os.ReadFile(out)
	require.NoError(t, err, "the start command reached neither the runtime nor a stop: %s", stderr.String())
	return string(data), ""
}

// TestEventSinksNeedMetricsThroughTheCommand pins Q21 at the command (UC-021 A5): an event-sinks entry makes `flagd
// start` ask the runtime to refuse event sinks without a telemetry provider that serves /metrics, and the same file
// with no entries, or with middleware only, does not.
func TestEventSinksNeedMetricsThroughTheCommand(t *testing.T) {
	tests := map[string]struct {
		file string
		want string
	}{
		"an event-sinks entry": {
			"event-sinks:\n  - type: webhook\n    url: https://hooks.example.com\n",
			"needsMetrics=true eventSinks=1 middleware=0\n",
		},
		"two entries":        {"event-sinks:\n  - type: webhook\n  - type: webhook\n", "needsMetrics=true eventSinks=2 middleware=0\n"},
		"no entries":         {"event-sinks: []\n", "needsMetrics=false eventSinks=0 middleware=0\n"},
		"no event-sinks key": {"port: 8013\n", "needsMetrics=false eventSinks=0 middleware=0\n"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			given, stopped := runExtensionStart(t, tt.file)
			require.Empty(t, stopped)
			require.Equal(t, tt.want, given)
		})
	}
}

// TestInvalidEventSinkSettingStops pins that the start command stops, with the type and what the distribution has
// named, when an entry names an event sink it lacks, before it reaches the runtime.
func TestInvalidEventSinkSettingStops(t *testing.T) {
	given, stopped := runExtensionStart(t, "event-sinks:\n  - type: kafka\n")
	require.Empty(t, given, "the runtime must not be reached")
	require.Contains(t, stopped, `event-sinks[0]`)
	require.Contains(t, stopped, `"kafka"`)
	require.Contains(t, stopped, "it has: webhook")
}
