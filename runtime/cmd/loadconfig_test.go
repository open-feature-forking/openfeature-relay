package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/runtime/internal/officialset"
	"github.com/stretchr/testify/require"
)

// harnessConfigs are the configurations the flagd test harness's launchpad starts flagd with.
func harnessConfigs(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob("../../test-harness/launchpad/configs/*.json")
	require.NoError(t, err)
	require.NotEmpty(t, paths, "the test harness is missing: git submodule update --init test-harness")
	sort.Strings(paths)
	return paths
}

// withoutConfigFileLine drops the "using config file" line that flagd start prints about the file it reads.
func withoutConfigFileLine(rendered string) string {
	lines := strings.Split(rendered, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(line, "stderr: using config file:") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// TestLoadConfigMatchesStart: the runtime.Config and the telemetry settings that LoadConfig builds from a configuration
// file are those flagd start builds from it. The command runs in a process of its own with the same file.
func TestLoadConfigMatchesStart(t *testing.T) {
	for _, path := range harnessConfigs(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			want := withoutConfigFileLine(runStartConfigCase(t, startConfigCase{
				name: path,
				args: []string{"start", "--config", path},
			}))

			cfg, comps, err := LoadConfig(path, officialset.Components(), nil)
			require.NoError(t, err)
			got := renderRuntimeConfig(cfg) + renderTelemetryConfig(comps.TelemetryConfig)
			require.Equal(t, want, got)
		})
	}
}

func TestLoadConfigReadsTheSettingsOfTheFile(t *testing.T) {
	cfg, _, err := LoadConfig(configNamed(t, "ssl.json"), officialset.Components(), nil)
	require.NoError(t, err)
	require.Equal(t, "/ssl/server-cert.pem", cfg.ServiceCertPath)
	require.Equal(t, "/ssl/server-key.pem", cfg.ServiceKeyPath)
	require.Equal(t, uint16(8013), cfg.ServicePort, "a setting the file does not set has the default of flagd start")

	cfg, _, err = LoadConfig(configNamed(t, "sync-payload.json"), officialset.Components(), nil)
	require.NoError(t, err)
	require.True(t, cfg.DisableSyncMetadata)
	require.Equal(t, map[string]any{"injectedmetadata": "set"}, cfg.ContextValues)
}

func configNamed(t *testing.T, name string) string {
	t.Helper()
	for _, path := range harnessConfigs(t) {
		if filepath.Base(path) == name {
			return path
		}
	}
	t.Fatalf("the test harness has no configuration %s", name)
	return ""
}

const rewrittenSource = "testbed:allFlags"

// allFlagsRewrite is a rewrite like the testbed's: the file source flags/allFlags.json becomes another source.
func allFlagsRewrite(source sync.SourceConfig) sync.SourceConfig {
	if source.Provider == "file" && source.URI == "flags/allFlags.json" {
		source.URI, source.Provider = rewrittenSource, "testbed"
	}
	return source
}

// TestLoadConfigRewriteTouchesOneSource: with the rule, the one source it names changes and nothing else does.
func TestLoadConfigRewriteTouchesOneSource(t *testing.T) {
	for _, path := range harnessConfigs(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			plain, plainComponents, err := LoadConfig(path, officialset.Components(), nil)
			require.NoError(t, err)
			rewritten, rewrittenComponents, err := LoadConfig(path, officialset.Components(), []SourceRewrite{allFlagsRewrite})
			require.NoError(t, err)

			require.Len(t, rewritten.SyncProviders, len(plain.SyncProviders))
			changed := 0
			for i, source := range plain.SyncProviders {
				if source.URI == "flags/allFlags.json" {
					changed++
					require.Equal(t, sync.SourceConfig{URI: rewrittenSource, Provider: "testbed"}, withoutOptions(rewritten.SyncProviders[i]),
						"the source is replaced")
					continue
				}
				require.Equal(t, source, rewritten.SyncProviders[i], "a source the rule does not name is as written")
			}

			// everything else of the configuration is untouched
			plain.SyncProviders, rewritten.SyncProviders = nil, nil
			require.Equal(t, plain, rewritten)
			require.Equal(t, plainComponents.TelemetryConfig, rewrittenComponents.TelemetryConfig)
		})
	}

	t.Run("the configurations that name the source are default, ssl and sync-payload", func(t *testing.T) {
		var named []string
		for _, path := range harnessConfigs(t) {
			cfg, _, err := LoadConfig(path, officialset.Components(), []SourceRewrite{allFlagsRewrite})
			require.NoError(t, err)
			for _, source := range cfg.SyncProviders {
				if source.URI == rewrittenSource {
					named = append(named, filepath.Base(path))
				}
			}
		}
		require.Equal(t, []string{"default.json", "ssl.json", "sync-payload.json"}, named, "metadata.json has no such source")
	})
}

func withoutOptions(source sync.SourceConfig) sync.SourceConfig {
	return sync.SourceConfig{URI: source.URI, Provider: source.Provider}
}

func TestLoadConfigRewritesRunInOrderOnEverySource(t *testing.T) {
	path := configNamed(t, "default.json")
	suffix := func(s string) SourceRewrite {
		return func(source sync.SourceConfig) sync.SourceConfig {
			source.URI += s
			return source
		}
	}
	cfg, _, err := LoadConfig(path, officialset.Components(), []SourceRewrite{suffix("+a"), suffix("+b")})
	require.NoError(t, err)
	for _, source := range cfg.SyncProviders {
		require.True(t, strings.HasSuffix(source.URI, "+a+b"), source.URI)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	t.Run("a file that is not there", func(t *testing.T) {
		_, _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json"), officialset.Components(), nil)
		require.ErrorContains(t, err, "missing.json")
	})

	t.Run("a setting of a component the distribution lacks", func(t *testing.T) {
		// flagd-full's components without the flag sync endpoint cannot take disable-sync-metadata
		without := officialset.Components()
		without.Endpoints = nil
		_, _, err := LoadConfig(configNamed(t, "sync-payload.json"), without, nil)
		require.ErrorContains(t, err, "disable-sync-metadata")
	})
}

// LoadConfig reads the file and the environment only: it leaves the command line's global state alone, so a process
// can load configurations repeatedly and each load is the same.
func TestLoadConfigIsRepeatableAndLeavesGlobalStateAlone(t *testing.T) {
	path := configNamed(t, "ssl.json")
	first, _, err := LoadConfig(path, officialset.Components(), nil)
	require.NoError(t, err)
	_, _, err = LoadConfig(configNamed(t, "default.json"), officialset.Components(), nil)
	require.NoError(t, err)
	second, _, err := LoadConfig(path, officialset.Components(), nil)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Empty(t, cfgFile, "the --config of the command is not touched")
}

const grpcSourceFile = `{"sources":[{"uri":"up:8015","provider":"grpc","selector":"a=b","grpc":{"keepAliveTime":30000,"selectorHeader":true,"fatalStatusCodes":["PERMISSION_DENIED"]}}]}`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flagd.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestLoadConfigGRPCObjectIsStrict: the grpc object of a source in the file reaches the source, and one that is
// malformed stops LoadConfig with an error that names the file, the source and the key.
func TestLoadConfigGRPCObjectIsStrict(t *testing.T) {
	t.Run("a valid object arrives on the source", func(t *testing.T) {
		cfg, _, err := LoadConfig(writeConfig(t, grpcSourceFile), officialset.Components(), nil)
		require.NoError(t, err)
		require.Len(t, cfg.SyncProviders, 1)
		require.Equal(t, &sync.GRPCSourceSettings{
			KeepAliveTime: 30000, SelectorHeader: true, FatalStatusCodes: []string{"PERMISSION_DENIED"},
		}, cfg.SyncProviders[0].GRPC)
	})

	for name, object := range map[string]string{
		"an unknown key": `{"keepAliveTimeMs":30000}`,
		"a wrong type":   `{"keepAliveTime":"30s"}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, `{"sources":[{"uri":"up:8015","provider":"grpc","grpc":`+object+`}]}`)
			_, _, err := LoadConfig(path, officialset.Components(), nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), path)
			require.Contains(t, err.Error(), `source "up:8015"`)
			require.Contains(t, strings.ToLower(err.Error()), "keepalivetime", "viper lower-cases the keys it reads")
		})
	}
}

// TestLoadConfigMatchesStartWithGRPCObject: flagd start and LoadConfig read the grpc object of a source alike.
func TestLoadConfigMatchesStartWithGRPCObject(t *testing.T) {
	want := withoutConfigFileLine(runStartConfigCase(t, startConfigCase{
		name:  "a grpc source with a grpc object",
		args:  []string{"start", "--config", "$HOME/flagd.json"},
		files: map[string]string{"flagd.json": grpcSourceFile},
	}))
	require.Contains(t, want, `"grpc":{"keepAliveTime":30000`, "start read the object")

	cfg, comps, err := LoadConfig(writeConfig(t, grpcSourceFile), officialset.Components(), nil)
	require.NoError(t, err)
	require.Equal(t, want, renderRuntimeConfig(cfg)+renderTelemetryConfig(comps.TelemetryConfig))
}
