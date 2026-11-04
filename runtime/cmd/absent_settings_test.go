package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/open-feature-forking/openfeature-relay/runtime"
	"github.com/stretchr/testify/require"
)

// The helper process below reads its case from these variables. They carry no FLAGD_ prefix, so flagd never reads them.
const (
	absentArgsEnv       = "RELAY_ABSENT_ARGS"
	absentComponentsEnv = "RELAY_ABSENT_COMPONENTS"
	absentHintEnv       = "RELAY_ABSENT_HINT"
)

// typedEndpoint is a service endpoint factory that only has a type.
type typedEndpoint struct{ name string }

func (e typedEndpoint) Type() string         { return e.name }
func (e typedEndpoint) Needs() service.Needs { return service.Needs{} }
func (e typedEndpoint) Create(service.Settings) (service.Endpoint, error) {
	return nil, errors.New("not started")
}

// otelLike is a telemetry provider factory that is not the noop one.
type otelLike struct{ telemetry.Factory }

func (otelLike) Type() string { return "otel" }

// TestAbsentSettingsHelper is not a test: the cases below run the test binary again with it, so that each gets fresh
// cobra and viper state. It executes the root command for a distribution made of the components named in the
// environment, and exits 0 with a line on standard error when startup reaches the runtime.
func TestAbsentSettingsHelper(_ *testing.T) {
	raw := os.Getenv(absentArgsEnv)
	if raw == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		panic(err)
	}
	var components distribution.Components
	for _, name := range strings.Fields(os.Getenv(absentComponentsEnv)) {
		if name == componentTelemetry {
			components.Telemetry = otelLike{telemetry.NoopFactory()}
			continue
		}
		components.Endpoints = append(components.Endpoints, typedEndpoint{name})
	}
	fromConfig = func(_ *logger.Logger, _ string, _ runtime.Config, _ distribution.Components) (*runtime.Runtime, error) {
		_, _ = os.Stderr.WriteString("reached the runtime\n")
		os.Exit(0)
		return nil, nil
	}
	rootCmd.SetArgs(args)
	Execute(distribution.Info{Name: "test-dist", Version: "v0.0.0", ComponentsHint: os.Getenv(absentHintEnv) == "on"},
		components, distribution.Inventory{})
}

type absentCase struct {
	// components are the component types the distribution contains.
	components string
	hint       bool
	args       []string
	env        map[string]string
	// file is the content of the configuration file in the home directory.
	file string
}

type absentResult struct {
	exit   int
	output string
}

func runAbsent(t *testing.T, c absentCase) absentResult {
	t.Helper()
	home := t.TempDir()
	if c.file != "" {
		require.NoError(t, os.WriteFile(filepath.Join(home, ".agent.yaml"), []byte(c.file), 0o600))
	}
	args, err := json.Marshal(append([]string{"start"}, c.args...))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAbsentSettingsHelper$")
	hint := "off"
	if c.hint {
		hint = "on"
	}
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"),
		absentArgsEnv + "=" + string(args), absentComponentsEnv + "=" + c.components, absentHintEnv + "=" + hint}
	for k, v := range c.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	result := absentResult{}
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit)
		result.exit = exit.ExitCode()
	}
	result.output = out.String()
	return result
}

const syncOnly = "flag-sync"

// TestAbsentComponentSettingStopsStartup pins UC-010 BR-001, A3 and UC-017 A2: a setting of a component the distribution
// does not contain stops startup, whichever way it is set, and the error names the setting and its components.
func TestAbsentComponentSettingStopsStartup(t *testing.T) {
	tests := map[string]struct {
		c    absentCase
		want []string
	}{
		"flag": {
			absentCase{components: syncOnly, hint: true, args: []string{"--context-from-header", "X-A=a"}},
			[]string{"--context-from-header (flag-evaluation, ofrep)"},
		},
		"flag at its default value": {
			absentCase{components: syncOnly, hint: true, args: []string{"--ofrep-port", "8016"}},
			[]string{"--ofrep-port (ofrep)"},
		},
		"environment variable": {
			absentCase{components: syncOnly, hint: true, env: map[string]string{"FLAGD_OFREP_SSE_ENABLED": "true"}},
			[]string{"FLAGD_OFREP_SSE_ENABLED (ofrep)"},
		},
		"configuration file key": {
			absentCase{components: syncOnly, hint: true, file: "port: 9001\n"},
			[]string{"port in the configuration file (flag-evaluation)"},
		},
		"configuration file key at its default value": {
			absentCase{components: syncOnly, hint: true, file: "ofrep-sse-enabled: true\n"},
			[]string{"ofrep-sse-enabled in the configuration file (ofrep)"},
		},
		"the telemetry provider": {
			absentCase{components: "flag-evaluation flag-sync ofrep", hint: true, args: []string{"--metrics-exporter", "otel"}},
			[]string{"--metrics-exporter (telemetry)"},
		},
		"no OFREP": {
			absentCase{components: "flag-evaluation flag-sync", hint: true, args: []string{"--ofrep-port", "9016"}},
			[]string{"--ofrep-port (ofrep)"},
		},
		"every setting in one error, sorted": {
			absentCase{
				components: syncOnly, hint: true, args: []string{"--port", "9013", "--context-from-header", "X-A=a"},
				env: map[string]string{"FLAGD_OFREP_PORT": "9016"},
			},
			[]string{"--context-from-header (flag-evaluation, ofrep), --port (flag-evaluation), FLAGD_OFREP_PORT (ofrep)"},
		},
		"no hint when the manifest turns it off": {
			absentCase{components: syncOnly, args: []string{"--port", "9013"}},
			[]string{"--port (flag-evaluation)"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := runAbsent(t, tt.c)
			require.Equal(t, 1, r.exit, r.output)
			require.NotContains(t, r.output, "reached the runtime")
			require.Contains(t, r.output, "the distribution test-dist does not contain the components of these settings: ")
			for _, want := range tt.want {
				require.Contains(t, r.output, want)
			}
			require.Equal(t, tt.c.hint, strings.Contains(r.output, `run "flagd components" to list the components of this distribution`), r.output)
		})
	}
}

// TestDefaultsNeverStop pins N3: a distribution that lacks components starts with the defaults of their settings, and
// the runtime's own settings never stop it. Neither does a setting of a component it contains.
func TestDefaultsNeverStop(t *testing.T) {
	tests := map[string]absentCase{
		"no component and no setting": {components: "", hint: true},
		"only the runtime's own settings": {
			components: "", hint: true, args: []string{"--log-format", "json", "--management-port", "9014", "--debug"},
			env: map[string]string{"FLAGD_STRICT_NUMERIC_RANGE": "true"}, file: "keep-alive-min-time: 5s\n",
		},
		"a setting of a component it contains": {components: syncOnly, hint: true, args: []string{"--sync-port", "9015"}},
		"a setting that belongs to several components, one of them contained": {
			components: syncOnly, hint: true, args: []string{"--cors-origin", "https://a.example"},
		},
		"a key of the configuration file it does not know": {components: syncOnly, hint: true, file: "event-sinks: []\n"},
	}
	for name, c := range tests {
		t.Run(name, func(t *testing.T) {
			r := runAbsent(t, c)
			require.Equal(t, 0, r.exit, r.output)
			require.Contains(t, r.output, "reached the runtime")
		})
	}
}

// TestUnknownSchemeHint pins that the unknown-scheme error points to the components command when the distribution's
// manifest asks for it, and keeps the text it has without.
func TestUnknownSchemeHint(t *testing.T) {
	const text = "invalid sync uri argument: nope://x, must start with 'file:', 'http(s)://', 'grpc(s)://', 'gs://', 'azblob://', 's3://' or 'core.openfeature.dev'"
	const hint = `; run "flagd components" to list the components of this distribution`

	with := runAbsent(t, absentCase{components: syncOnly, hint: true, args: []string{"--uri", "nope://x"}})
	require.Equal(t, 1, with.exit, with.output)
	require.Contains(t, with.output, text+hint)

	without := runAbsent(t, absentCase{components: syncOnly, args: []string{"--uri", "nope://x"}})
	require.Equal(t, 1, without.exit, without.output)
	require.Contains(t, without.output, text)
	require.NotContains(t, without.output, hint)
}

// prefixSync is a sync component that handles the sources starting with its schemes.
type prefixSync struct{ schemes []string }

func (p prefixSync) Type() string      { return p.schemes[0] }
func (p prefixSync) Schemes() []string { return p.schemes }
func (p prefixSync) ParseURI(uri string) (coresync.SourceConfig, bool) {
	for _, s := range p.schemes {
		if strings.HasPrefix(uri, s) {
			return coresync.SourceConfig{URI: uri, Provider: p.schemes[0]}, true
		}
	}
	return coresync.SourceConfig{}, false
}

// TestUnsupportedSourceNamesTheSchemes pins UC-010 A2: a distribution with the hint on names the source whose scheme it
// does not support and the schemes its sync providers handle; with the hint off the error keeps its text.
func TestUnsupportedSourceNamesTheSchemes(t *testing.T) {
	defer func(hint bool, name string) { ComponentsHint, Distribution = hint, name }(ComponentsHint, Distribution)
	Distribution = "flagd-minimal"
	c := distribution.Components{Sync: []coresync.Factory{prefixSync{[]string{"file:"}}, prefixSync{[]string{"http://", "https://"}}}}
	original := errors.New("invalid sync uri argument: s3://bucket/flags.json, must start with 'file:' or 's3://'")
	uris := []string{"file:/flags.json", "s3://bucket/flags.json"}

	ComponentsHint = true
	err := unsupportedSourceError(original, uris, c)
	require.EqualError(t, err, "invalid sync uri argument: s3://bucket/flags.json, the distribution flagd-minimal has no sync provider for it and handles "+
		`file:, http://, https://; run "flagd components" to list the components of this distribution`)

	ComponentsHint = false
	require.Equal(t, original, unsupportedSourceError(original, uris, c))
}
