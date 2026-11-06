package distributions

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// standardDistribution is one of the standard distributions of this repository (UC-010) and the port flags its
// endpoints have. A distribution passed a port flag of an endpoint it lacks stops at startup.
type standardDistribution struct {
	name      string
	portFlags []string
	evaluates bool
}

var (
	allPortFlags = []string{"port", "management-port", "sync-port", "ofrep-port"}
	standard     = []standardDistribution{
		{"flagd-minimal", allPortFlags, true},
		{"flagd-minimal-otel", allPortFlags, true},
		{"flagd-cloud", allPortFlags, true},
		{"flagd-kubernetes", allPortFlags, true},
		{"flagd-sync", []string{"management-port", "sync-port"}, false},
	}
)

// manifestOf is the path of the manifest of a distribution of this repository.
func manifestOf(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(root(t), "distributions", name, "manifest.yaml")
}

// builtBinary builds the distribution with the Builder and returns its binary.
func builtBinary(t *testing.T, name string) string {
	t.Helper()
	path := manifestOf(t, name)
	b := build(t, path)
	require.NoError(t, b.err, b.out)
	return binary(t, path)
}

// importedModules returns the modules whose packages the generated source of a built distribution imports,
// directly or not: what go list -deps reports in its build directory.
func importedModules(t *testing.T, name string) map[string]bool {
	t.Helper()
	_ = builtBinary(t, name)
	cmd := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{.Path}}{{end}}", "./...")
	cmd.Dir = filepath.Join(filepath.Dir(manifestOf(t, name)), "build")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	require.NoError(t, err)
	modules := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			modules[line] = true
		}
	}
	return modules
}

// forbiddenModules are the modules a standard distribution must not link, and its packages must not import (Q3). None of
// them links the deprecated flag evaluation endpoint, which only flagd-full and flagd-testbed list.
var forbiddenModules = map[string][]string{
	// NFR-004
	"flagd-minimal": {relay + "service/flag-evaluation-legacy", "k8s.io/", "cloud.google.com/", "github.com/aws/", "github.com/Azure/", "go.opentelemetry.io/otel/sdk"},
	// flagd-minimal plus OpenTelemetry: the SDK is the one module of NFR-004 it adds
	"flagd-minimal-otel": {relay + "service/flag-evaluation-legacy", "k8s.io/", "cloud.google.com/", "github.com/aws/", "github.com/Azure/"},
	// no operator source, so no Kubernetes client
	"flagd-cloud": {relay + "service/flag-evaluation-legacy", "k8s.io/", "sigs.k8s.io/"},
	// no cloud storage source
	"flagd-kubernetes": {relay + "service/flag-evaluation-legacy", "cloud.google.com/go/storage", "github.com/aws/", "github.com/Azure/"},
	// NFR-011
	"flagd-sync": {relay + "service/flag-evaluation-legacy", relay + "evaluator/", "github.com/diegoholiveira/jsonlogic"},
}

// TestStandardModuleGraphs pins what each standard distribution links, as go version -m lists it (what a scanner of a
// release reads), and what its packages import: neither holds a module the distribution is built to go without
// (NFR-004, NFR-011, UC-010 BR-004).
func TestStandardModuleGraphs(t *testing.T) {
	for _, d := range standard {
		t.Run(d.name, func(t *testing.T) {
			linkedModules := modulesOf(t, builtBinary(t, d.name))
			imported := importedModules(t, d.name)
			for _, prefix := range forbiddenModules[d.name] {
				for _, set := range []struct {
					what    string
					modules map[string]bool
				}{{"links", linkedModules}, {"imports", imported}} {
					for module := range set.modules {
						require.False(t, strings.HasPrefix(module, prefix), "%s %s %s", d.name, set.what, module)
					}
				}
			}
			if d.evaluates {
				require.True(t, linkedModules[relay+"evaluator/flagd"], "%s evaluates with flagd's evaluator", d.name)
			}
			require.True(t, linkedModules[relay+"configformat/flagd"], "%s reads flagd's configuration format", d.name)
		})
	}
}

// TestMinimalOtelLinksTheSDK pins that flagd-minimal-otel is flagd-minimal plus OpenTelemetry: its binary links the
// OpenTelemetry SDK, which flagd-minimal does not (NFR-004), and none of the modules the cloud and the operator bring.
func TestMinimalOtelLinksTheSDK(t *testing.T) {
	linkedModules := modulesOf(t, builtBinary(t, "flagd-minimal-otel"))
	require.True(t, linkedModules["go.opentelemetry.io/otel/sdk"], "flagd-minimal-otel records telemetry with the OpenTelemetry SDK")
	for module := range linkedModules {
		for _, prefix := range []string{"k8s.io/", "cloud.google.com/", "github.com/aws/", "github.com/Azure/"} {
			require.False(t, strings.HasPrefix(module, prefix), "flagd-minimal-otel links %s", module)
		}
	}
}

// TestMinimalSizeRatio pins NFR-003: the flagd-minimal binary is at most half the size of the flagd-full binary.
func TestMinimalSizeRatio(t *testing.T) {
	size := func(name string) int64 {
		info, err := os.Stat(builtBinary(t, name))
		require.NoError(t, err)
		return info.Size()
	}
	minimal, full := size("flagd-minimal"), size("flagd-full")
	t.Logf("flagd-minimal %d bytes, flagd-full %d bytes: %.1f%%", minimal, full, 100*float64(minimal)/float64(full))
	require.LessOrEqual(t, 2*minimal, full, "flagd-minimal is more than half the size of flagd-full")
}

// writeFlagFile writes a flag configuration with one flag and returns its path.
func writeFlagFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flags.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"flags":{"greeting":{"state":"ENABLED","defaultVariant":"hello","variants":{"hello":"hello","bye":"bye"}}}}`), 0o600))
	return path
}

// runBinary runs the binary with an empty home directory and returns its exit status and output.
func runBinary(t *testing.T, bin string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err == nil {
		return 0, out.String()
	}
	exit, ok := err.(*exec.ExitError)
	require.True(t, ok, err)
	return exit.ExitCode(), out.String()
}

// TestMinimalRecordsNoTelemetry pins UC-010 BR-003 and FR-024: flagd-minimal has the noop telemetry provider, so the
// management port serves no metrics, and a setting of the OpenTelemetry provider stops it at startup.
func TestMinimalRecordsNoTelemetry(t *testing.T) {
	ports := startDistribution(t, "flagd-minimal", allPortFlags, nil, writeFlagFile(t))
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", ports["management-port"])) //nolint:noctx // a local request
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "the noop telemetry provider serves no /metrics")

	exit, out := runBinary(t, builtBinary(t, "flagd-minimal"), "start", "--metrics-exporter", "otel", "--otel-collector-uri", "127.0.0.1:1")
	require.Equal(t, 1, exit, out)
	require.Contains(t, out, "--metrics-exporter (telemetry)")
	require.Contains(t, out, "--otel-collector-uri (telemetry)")
}

// TestVersionVerboseNamesEachDistribution pins UC-010 step 8: the running binary names the distribution and its
// manifest version.
func TestVersionVerboseNamesEachDistribution(t *testing.T) {
	for _, name := range []string{"flagd-full", "flagd-minimal", "flagd-minimal-otel", "flagd-cloud", "flagd-kubernetes", "flagd-sync"} {
		t.Run(name, func(t *testing.T) {
			exit, out := runBinary(t, builtBinary(t, name), "version", "--verbose")
			require.Zero(t, exit, out)
			require.Contains(t, out, "distribution: "+name+" 0.0.0\n")
		})
	}
}

// TestStartsWithFlagdFullDefaults pins UC-010 BR-002 and step 4: every standard distribution takes flagd-full's start
// flags with their names and defaults, and starts on a file source with the ports of its endpoints.
func TestStartsWithFlagdFullDefaults(t *testing.T) {
	_, fullHelp := runBinary(t, builtBinary(t, "flagd-full"), "start", "--help")
	for _, d := range standard {
		t.Run(d.name, func(t *testing.T) {
			exit, help := runBinary(t, builtBinary(t, d.name), "start", "--help")
			require.Zero(t, exit, help)
			require.Equal(t, fullHelp, help, "the flags of start, with their defaults")

			ports := startDistribution(t, d.name, d.portFlags, nil, writeFlagFile(t))
			require.Len(t, ports, len(d.portFlags))
		})
	}
}
