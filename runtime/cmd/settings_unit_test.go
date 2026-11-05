package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// withConfigState restores the configuration state initConfig and configFileKeys read and write.
func withConfigState(t *testing.T) {
	t.Helper()
	oldFile, oldDebug, oldArgs := cfgFile, Debug, os.Args
	t.Cleanup(func() {
		cfgFile, Debug, os.Args = oldFile, oldDebug, oldArgs
		viper.Reset()
		viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
		viper.SetEnvPrefix("FLAGD")
		bindFlags(startCmd.Flags())
	})
	t.Setenv("FLAGD_DEBUG", "")
	require.NoError(t, os.Unsetenv("FLAGD_DEBUG"))
}

func withHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func writeFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
	return path
}

// stderrOf returns what f writes to standard error.
func stderrOf(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()
	f()
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func TestConfigFileKeys(t *testing.T) {
	withConfigState(t)
	dir := t.TempDir()

	cfgFile = writeFile(t, dir, "flagd.yaml", "port: 8013\nsync-port: 8015\n")
	require.ElementsMatch(t, []string{"port", "sync-port"}, configFileKeys())

	cfgFile = filepath.Join(dir, "missing.yaml")
	require.Nil(t, configFileKeys(), "a file that cannot be read has no keys")

	cfgFile = ""
	withHome(t, dir)
	writeFile(t, dir, ".agent.yaml", "ofrep-port: 8016\n")
	require.Equal(t, []string{"ofrep-port"}, configFileKeys(), "without --config, .agent.yaml in the home directory")

	withHome(t, t.TempDir())
	require.Nil(t, configFileKeys())
}

func TestInitConfig(t *testing.T) {
	t.Run("the file of --config", func(t *testing.T) {
		withConfigState(t)
		os.Args = []string{"flagd", "start"}
		cfgFile = writeFile(t, t.TempDir(), "flagd.yaml", "debug: true\n")
		Debug = false

		out := stderrOf(t, initConfig)

		require.Equal(t, "using config file: "+cfgFile+"\n", out)
		require.Equal(t, cfgFile, viper.ConfigFileUsed())
		require.False(t, Debug, "initConfig reads debug before the file, so the file's debug key does not set it")
	})

	t.Run(".agent.yaml in the home directory", func(t *testing.T) {
		withConfigState(t)
		os.Args = []string{"flagd", "start"}
		cfgFile = ""
		home := t.TempDir()
		withHome(t, home)
		file := writeFile(t, home, ".agent.yaml", "port: 8013\n")

		out := stderrOf(t, initConfig)

		require.Equal(t, "using config file: "+file+"\n", out)
	})

	t.Run("debug from the environment", func(t *testing.T) {
		withConfigState(t)
		os.Args = []string{"flagd", "start"}
		cfgFile = filepath.Join(t.TempDir(), "missing.yaml")
		t.Setenv("FLAGD_DEBUG", "true")
		Debug = false

		_ = stderrOf(t, initConfig)

		require.True(t, Debug)
	})

	t.Run("no file", func(t *testing.T) {
		withConfigState(t)
		os.Args = []string{"flagd", "start"}
		cfgFile = filepath.Join(t.TempDir(), "missing.yaml")
		Debug = true

		out := stderrOf(t, initConfig)

		require.Empty(t, out, "a file that cannot be read is ignored without a word")
		require.False(t, Debug)
	})
}

func TestAbsentSettingsErrorUnit(t *testing.T) {
	oldDist, oldHint := Distribution, ComponentsHint
	t.Cleanup(func() { Distribution, ComponentsHint = oldDist, oldHint })
	Distribution, ComponentsHint = "test-dist", false

	none := distribution.Components{}
	evaluation := distribution.Components{Endpoints: []service.Factory{typedEndpoint{componentFlagEvaluation}}}

	require.NoError(t, absentSettingsError(none, nil))
	require.NoError(t, absentSettingsError(none, []explicitSetting{{"management-port", "--management-port"}}),
		"a runtime setting names no component")
	require.NoError(t, absentSettingsError(evaluation, []explicitSetting{{"port", "--port"}}))
	require.NoError(t, absentSettingsError(evaluation, []explicitSetting{{"context-from-header", "--context-from-header"}}),
		"one of the setting's components is enough")

	err := absentSettingsError(evaluation, []explicitSetting{
		{"sync-port", "FLAGD_SYNC_PORT"}, {"otel-collector-uri", "--otel-collector-uri"}, {"port", "--port"},
		{"server-cert-path", "server-cert-path in the configuration file"},
	})
	require.EqualError(t, err, "the distribution test-dist does not contain the components of these settings: "+
		"--otel-collector-uri ("+componentTelemetry+"), FLAGD_SYNC_PORT ("+componentFlagSync+")")

	noop := distribution.Components{Telemetry: telemetry.NoopFactory()}
	require.Error(t, absentSettingsError(noop, []explicitSetting{{"metrics-exporter", "--metrics-exporter"}}),
		"the noop telemetry provider is no telemetry component")
	require.NoError(t, absentSettingsError(distribution.Components{Telemetry: otelLike{telemetry.NoopFactory()}},
		[]explicitSetting{{"metrics-exporter", "--metrics-exporter"}}))

	ComponentsHint = true
	err = absentSettingsError(none, []explicitSetting{{"ofrep-port", "--ofrep-port"}})
	require.EqualError(t, err, "the distribution test-dist does not contain the components of these settings: "+
		"--ofrep-port ("+componentOFREP+")"+componentsHint)
}
