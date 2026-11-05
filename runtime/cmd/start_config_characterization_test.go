package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/runtime"
	"github.com/open-feature/flagd/flagd/components"
	"github.com/stretchr/testify/require"
)

var updateStartConfig = flag.Bool("update-start-config", false, "rewrite testdata/start_config.golden from the current start command")

const startConfigGolden = "testdata/start_config.golden"

// The helper process reads its case from these variables. They carry no FLAGD_ prefix, so flagd never reads them.
const (
	startConfigOutEnv  = "RELAY_START_CONFIG_OUT"
	startConfigArgsEnv = "RELAY_START_CONFIG_ARGS"
)

// TestStartConfigHelper is not a test: the characterization below runs the test binary again with it, so that each
// case gets fresh cobra and viper state. It executes the root command and writes the runtime.Config that `flagd start`
// hands to the runtime, instead of starting it.
func TestStartConfigHelper(_ *testing.T) {
	out := os.Getenv(startConfigOutEnv)
	if out == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv(startConfigArgsEnv)), &args); err != nil {
		panic(err)
	}
	fromConfig = func(_ *logger.Logger, _ string, cfg runtime.Config, components distribution.Components) (*runtime.Runtime, error) {
		if err := os.WriteFile(out, []byte(renderRuntimeConfig(cfg)+renderTelemetryConfig(components.TelemetryConfig)), 0o600); err != nil {
			panic(err)
		}
		os.Exit(0)
		return nil, nil
	}
	rootCmd.SetArgs(args)
	Execute(distribution.Info{Name: "flagd-full", Version: "v9.9.9", Commit: "abc1234", Date: "2026-01-02T03:04:05Z"},
		components.Components(), distribution.Inventory{})
}

// renderTelemetryConfig prints the settings of the telemetry provider that the telemetry flags map to. They were
// fields of the runtime.Config before the telemetry provider, and operators set them the same way.
func renderTelemetryConfig(settings map[string]any) string {
	keys := make([]string, 0, len(settings))
	for k := range settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "telemetry.%s: %#v\n", k, settings[k])
	}
	return b.String()
}

// renderRuntimeConfig prints every field of the runtime.Config in declaration order. Sync providers are JSON, so that
// pointer fields print their content.
func renderRuntimeConfig(cfg runtime.Config) string {
	var b strings.Builder
	v := reflect.ValueOf(cfg)
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		field := v.Field(i)
		switch {
		case name == "SyncProviders":
			raw, err := json.Marshal(field.Interface())
			if err != nil {
				panic(err)
			}
			fmt.Fprintf(&b, "%s: %s\n", name, raw)
		case field.Type() == reflect.TypeFor[time.Duration]():
			fmt.Fprintf(&b, "%s: %s\n", name, field.Interface())
		case field.Kind() == reflect.String, field.Kind() == reflect.Slice, field.Kind() == reflect.Map:
			fmt.Fprintf(&b, "%s: %#v\n", name, field.Interface())
		default:
			fmt.Fprintf(&b, "%s: %v\n", name, field.Interface())
		}
	}
	return b.String()
}

type startConfigCase struct {
	name string
	args []string
	env  map[string]string
	// files are written into the case's home directory before the run, keyed by their path relative to it.
	files map[string]string
	// fails marks a case where flagd start exits non-zero before it builds the runtime, with a message that contains this.
	fails string
}

// The values differ per source and per setting, so that a setting read from the wrong flag, the wrong source or not
// at all changes the golden file.
var startConfigCases = []startConfigCase{
	{
		name: "defaults",
		args: []string{"start"},
	},
	{
		name: "every flag on the command line",
		args: []string{
			"start",
			"--management-port", "9014", "--port", "9013", "--sync-port", "9015", "--ofrep-port", "9016",
			"--sync-http-enabled=false",
			"--ofrep-sse-enabled=false", "--ofrep-sse-inactivity-delay", "77", "--ofrep-sse-public-url", "https://cli.example",
			"--socket-path", "/cli/evaluation.sock", "--sync-socket-path", "/cli/sync.sock",
			"--server-cert-path", "/cli/server.crt", "--server-key-path", "/cli/server.key",
			"--uri", "file:/cli/a.json", "--uri", "file:/cli/b.json",
			"--cors-origin", "https://a.cli,https://b.cli",
			"--sources", `[{"uri":"cli.example:8015","provider":"grpc","selector":"cli"}]`,
			"--log-format", "json",
			"--metrics-exporter", "otel", "--otel-collector-uri", "cli-collector:4317",
			"--otel-cert-path", "/cli/otel.crt", "--otel-key-path", "/cli/otel.key", "--otel-ca-path", "/cli/otel-ca.crt",
			"--otel-reload-interval", "2h",
			"--context-value", "cliKey1=cliValue1,cliKey2=cliValue2",
			"--context-from-header", "X-Cli-A=cliA,X-Cli-B=cliB",
			"--stream-deadline", "3s", "--disable-sync-metadata", "--strict-numeric-range",
			"--max-request-body", "4321", "--max-request-header", "8765",
			"--keep-alive-min-time", "45s", "--keep-alive-permit-without-stream=false",
			"--debug",
		},
	},
	{
		name: "every flag as an environment variable",
		args: []string{"start"},
		env: map[string]string{
			"FLAGD_MANAGEMENT_PORT": "10014", "FLAGD_PORT": "10013", "FLAGD_SYNC_PORT": "10015", "FLAGD_OFREP_PORT": "10016",
			"FLAGD_SYNC_HTTP_ENABLED":                "false",
			"FLAGD_OFREP_SSE_ENABLED":                "false",
			"FLAGD_OFREP_SSE_INACTIVITY_DELAY":       "88",
			"FLAGD_OFREP_SSE_PUBLIC_URL":             "https://env.example",
			"FLAGD_SOCKET_PATH":                      "/env/evaluation.sock",
			"FLAGD_SYNC_SOCKET_PATH":                 "/env/sync.sock",
			"FLAGD_SERVER_CERT_PATH":                 "/env/server.crt",
			"FLAGD_SERVER_KEY_PATH":                  "/env/server.key",
			"FLAGD_URI":                              "file:/env/a.json file:/env/b.json",
			"FLAGD_CORS_ORIGIN":                      "https://a.env https://b.env",
			"FLAGD_SOURCES":                          `[{"uri":"env.example:8015","provider":"grpc","selector":"env"}]`,
			"FLAGD_LOG_FORMAT":                       "json",
			"FLAGD_METRICS_EXPORTER":                 "otel",
			"FLAGD_OTEL_COLLECTOR_URI":               "env-collector:4317",
			"FLAGD_OTEL_CERT_PATH":                   "/env/otel.crt",
			"FLAGD_OTEL_KEY_PATH":                    "/env/otel.key",
			"FLAGD_OTEL_CA_PATH":                     "/env/otel-ca.crt",
			"FLAGD_OTEL_RELOAD_INTERVAL":             "3h",
			"FLAGD_CONTEXT_VALUE":                    `{"envKey1":"envValue1"}`,
			"FLAGD_CONTEXT_FROM_HEADER":              `{"X-Env-A":"envA"}`,
			"FLAGD_STREAM_DEADLINE":                  "4s",
			"FLAGD_DISABLE_SYNC_METADATA":            "true",
			"FLAGD_STRICT_NUMERIC_RANGE":             "true",
			"FLAGD_MAX_REQUEST_BODY":                 "5432",
			"FLAGD_MAX_REQUEST_HEADER":               "9876",
			"FLAGD_KEEP_ALIVE_MIN_TIME":              "46s",
			"FLAGD_KEEP_ALIVE_PERMIT_WITHOUT_STREAM": "false",
			"FLAGD_DEBUG":                            "true",
			// --config has no viper key, so this file is never read.
			"FLAGD_CONFIG": "$HOME/ignored.yaml",
		},
		files: map[string]string{"ignored.yaml": "port: 1\n"},
	},
	{
		name: "every key in the configuration file given with --config",
		args: []string{"start", "--config", "$HOME/flagd.yaml"},
		files: map[string]string{"flagd.yaml": `management-port: 11014
port: 11013
sync-port: 11015
ofrep-port: 11016
sync-http-enabled: false
ofrep-sse-enabled: false
ofrep-sse-inactivity-delay: 99
ofrep-sse-public-url: https://file.example
socket-path: /file/evaluation.sock
sync-socket-path: /file/sync.sock
server-cert-path: /file/server.crt
server-key-path: /file/server.key
uri:
  - file:/file/a.json
  - file:/file/b.json
cors-origin:
  - https://a.file
  - https://b.file
sources:
  - uri: file.example:8015
    provider: grpc
    selector: file
log-format: json
metrics-exporter: otel
otel-collector-uri: file-collector:4317
otel-cert-path: /file/otel.crt
otel-key-path: /file/otel.key
otel-ca-path: /file/otel-ca.crt
otel-reload-interval: 5h
context-value:
  fileKey1: fileValue1
context-from-header:
  X-File-A: fileA
stream-deadline: 5s
disable-sync-metadata: true
strict-numeric-range: true
max-request-body: 6543
max-request-header: 10987
keep-alive-min-time: 47s
keep-alive-permit-without-stream: false
debug: true
`},
	},
	{
		name:  "the default configuration file in the home directory",
		args:  []string{"start"},
		files: map[string]string{".agent.yaml": "port: 12013\nsources:\n  - uri: home.example:8015\n    provider: grpc\n"},
	},
	{
		name:  "command line over environment over configuration file",
		args:  []string{"start", "--config", "$HOME/flagd.yaml", "--port", "13013"},
		env:   map[string]string{"FLAGD_PORT": "14013", "FLAGD_MANAGEMENT_PORT": "14014"},
		files: map[string]string{"flagd.yaml": "port: 15013\nmanagement-port: 15014\nsync-port: 15015\n"},
	},
	// Upstream flagd 3fa8064e (#2107): a port setting that is no integer in [0, 65535] stops flagd start, where it
	// used to become port 0. flagd-full follows flagd.
	{
		name:  "a Kubernetes service link as FLAGD_PORT",
		args:  []string{"start"},
		env:   map[string]string{"FLAGD_PORT": "tcp://10.0.0.1:8015"},
		fails: `invalid port value for 'port': "tcp://10.0.0.1:8015" (this can be caused by Kubernetes service links, set enableServiceLinks: false on the pod)`,
	},
	{
		name:  "a non-numeric FLAGD_SYNC_PORT",
		args:  []string{"start"},
		env:   map[string]string{"FLAGD_SYNC_PORT": "abc"},
		fails: "invalid port value for 'sync-port'",
	},
	{
		name:  "a port out of range in the configuration file",
		args:  []string{"start", "--config", "$HOME/flagd.yaml"},
		files: map[string]string{"flagd.yaml": "ofrep-port: 70000\n"},
		fails: "invalid port value for 'ofrep-port'",
	},
}

// runStartConfigCase runs the helper with only the case's environment and an empty home directory, and returns what
// the start command printed to stderr about configuration files, then the runtime.Config it built.
func runStartConfigCase(t *testing.T, c startConfigCase) string {
	t.Helper()
	home := t.TempDir()
	expand := func(s string) string { return strings.ReplaceAll(s, "$HOME", home) }
	for name, content := range c.files {
		require.NoError(t, os.WriteFile(filepath.Join(home, name), []byte(content), 0o600))
	}
	args := make([]string, len(c.args))
	for i, a := range c.args {
		args[i] = expand(a)
	}
	rawArgs, err := json.Marshal(args)
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), "config.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStartConfigHelper$")
	cmd.Env = []string{
		"HOME=" + home, "PATH=" + os.Getenv("PATH"),
		startConfigOutEnv + "=" + out, startConfigArgsEnv + "=" + string(rawArgs),
	}
	for k, v := range c.env {
		cmd.Env = append(cmd.Env, k+"="+expand(v))
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if c.fails != "" {
		var exitErr *exec.ExitError
		require.ErrorAs(t, cmd.Run(), &exitErr, "case %q must stop flagd start: %s", c.name, stderr.String())
		require.NotZero(t, exitErr.ExitCode())
		require.NoFileExists(t, out, "case %q reached the runtime", c.name)
		for _, line := range strings.Split(stderr.String(), "\n") {
			if i := strings.Index(line, "invalid port value"); i >= 0 {
				require.Contains(t, line[i:], c.fails)
				return fmt.Sprintf("exit: non-zero\nstderr: %s\n", line[i:])
			}
		}
		require.FailNow(t, "no port error on stderr", stderr.String())
	}
	require.NoError(t, cmd.Run(), "start command of case %q: %s", c.name, stderr.String())

	var b strings.Builder
	for _, line := range strings.Split(stderr.String(), "\n") {
		if strings.HasPrefix(line, "using config file:") {
			fmt.Fprintf(&b, "stderr: %s\n", strings.ReplaceAll(line, home, "$HOME"))
		}
	}
	config, err := os.ReadFile(out)
	require.NoError(t, err, "case %q did not reach the runtime: %s", c.name, stderr.String())
	b.Write(config)
	return b.String()
}

// TestStartConfigCharacterization pins the runtime.Config that `flagd start` builds from the command line, the
// FLAGD_ environment variables and the configuration file, through the root command's initConfig. That mapping is
// what operators set, and a flagd-full built by the Builder must keep it (UC-001 Q15 and the drop-in rule), quirks
// included: --otel-reload-interval is bound to no viper key, so it is never read and the setting stays 0.
func TestStartConfigCharacterization(t *testing.T) {
	var out strings.Builder
	for _, c := range startConfigCases {
		fmt.Fprintf(&out, "== %s\n%s\n", c.name, runStartConfigCase(t, c))
	}

	if *updateStartConfig {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(startConfigGolden, []byte(out.String()), 0o600))
		return
	}
	want, err := os.ReadFile(startConfigGolden)
	require.NoError(t, err, "record the current mapping with `go test -run TestStartConfigCharacterization -update-start-config`")
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	require.Equal(t, string(want), out.String())
}
