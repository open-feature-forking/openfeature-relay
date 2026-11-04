// Package distributions builds the distributions of this repository with the Builder and checks what it made
// (UC-001): the selected components and only those are linked (BR-005, NFR-004), every relay module of a build list is
// replaced by its directory (Q14), and a manifest that forgets one is reported.
package distributions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	relay = "github.com/open-feature-forking/openfeature-relay/"
	flagd = "github.com/open-feature/flagd/flagd"
)

// infrastructure are the relay modules every distribution links besides the components it selects: the core, the
// runtime that serves them, the endpoints' shared helpers, and the sync builder the runtime dispatches sources with.
var infrastructure = []string{relay + "core", relay + "runtime", relay + "service/transport", relay + "sync/builder"}

// root is the repository's directory.
func root(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	return dir
}

type built struct {
	dir string // the generated source and binary
	out string
	err error
}

var (
	buildsMu sync.Mutex
	builds   = map[string]*built{}
)

// build runs the Builder on the manifest once per test binary and returns what it made. The Builder runs outside any
// workspace, as the Distribution Builder runs it.
func build(t *testing.T, manifestPath string) *built {
	t.Helper()
	buildsMu.Lock()
	defer buildsMu.Unlock()
	if b, ok := builds[manifestPath]; ok {
		return b
	}
	cmd := exec.Command("go", "run", "./cmd/relay-builder", "--manifest", manifestPath, "--output", filepath.Join(filepath.Dir(manifestPath), "build"))
	cmd.Dir = filepath.Join(root(t), "builder")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	b := &built{dir: filepath.Join(filepath.Dir(manifestPath), "build"), out: string(out), err: err}
	builds[manifestPath] = b
	return b
}

type manifestFile struct {
	Dist struct {
		Binary string `yaml:"binary"`
	} `yaml:"dist"`
	SyncProviders        []entry `yaml:"sync_providers"`
	ConfigurationFormats []entry `yaml:"configuration_formats"`
	Evaluators           []entry `yaml:"evaluators"`
	Engines              []entry `yaml:"engines"`
	ServiceEndpoints     []entry `yaml:"service_endpoints"`
	Telemetry            *entry  `yaml:"telemetry"`
	Middleware           []entry `yaml:"middleware"`
	EventSinks           []entry `yaml:"event_sinks"`
	Runtime              *entry  `yaml:"runtime"`
	Cmd                  *entry  `yaml:"cmd"`
	Replaces             []entry `yaml:"replaces"`
}

type entry struct {
	Module string `yaml:"module"`
}

func (m manifestFile) selected() []string {
	var modules []string
	for _, group := range [][]entry{m.SyncProviders, m.ConfigurationFormats, m.Evaluators, m.Engines, m.ServiceEndpoints, m.Middleware, m.EventSinks} {
		for _, e := range group {
			modules = append(modules, e.Module)
		}
	}
	for _, e := range []*entry{m.Telemetry, m.Runtime, m.Cmd} {
		if e != nil {
			modules = append(modules, e.Module)
		}
	}
	return modules
}

func readManifest(t *testing.T, path string) manifestFile {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var m manifestFile
	require.NoError(t, yaml.Unmarshal(data, &m))
	return m
}

func binary(t *testing.T, path string) string {
	t.Helper()
	m := readManifest(t, path)
	name := m.Dist.Binary
	if name == "" {
		name = "flagd"
	}
	return filepath.Join(filepath.Dir(path), "build", name)
}

// linked returns the modules a binary links, from go version -m.
func linked(t *testing.T, binaryPath string) []string {
	t.Helper()
	out, err := exec.Command("go", "version", "-m", binaryPath).Output()
	require.NoError(t, err)
	var modules []string
	for _, line := range strings.Split(string(out), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "dep" {
			modules = append(modules, fields[1])
		}
	}
	return modules
}

func isRelay(module string) bool { return strings.HasPrefix(module, relay) || module == flagd }

// TestOnlySelectedComponents (BR-005) pins that the binary links the selected relay modules plus only the
// infrastructure ones, and that nothing else of the repository comes along.
func TestOnlySelectedComponents(t *testing.T) {
	for _, name := range []string{"ofrep-only", "minimal-shape"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root(t), "test", "distributions", name, "manifest.yaml")
			b := build(t, path)
			require.NoError(t, b.err, b.out)

			allowed := map[string]bool{}
			for _, m := range append(readManifest(t, path).selected(), infrastructure...) {
				allowed[m] = true
			}
			for _, module := range linked(t, binary(t, path)) {
				if isRelay(module) {
					require.True(t, allowed[module], "%s is linked, but the manifest does not select it", module)
				}
			}
		})
	}
}

// TestMinimalShapeLinksNoPlatformSDK (NFR-004) pins that file and http sync, flagd's format and evaluator, the flag
// evaluation and OFREP endpoints and no telemetry link no Kubernetes, cloud or OpenTelemetry SDK module.
func TestMinimalShapeLinksNoPlatformSDK(t *testing.T) {
	path := filepath.Join(root(t), "test", "distributions", "minimal-shape", "manifest.yaml")
	b := build(t, path)
	require.NoError(t, b.err, b.out)

	for _, module := range linked(t, binary(t, path)) {
		for _, forbidden := range []string{"k8s.io/", "cloud.google.com/", "github.com/aws/", "github.com/Azure/", "go.opentelemetry.io/otel/sdk", "github.com/prometheus/"} {
			require.False(t, strings.HasPrefix(module, forbidden), "the minimal shape links %s", module)
		}
	}
}

// manifests lists the manifests of the distributions of this repository.
func manifests(t *testing.T) []string {
	t.Helper()
	var paths []string
	for _, pattern := range []string{
		filepath.Join(root(t), "distributions", "*", "manifest.yaml"),
		filepath.Join(root(t), "test", "distributions", "*", "manifest.yaml"),
	} {
		found, err := filepath.Glob(pattern)
		require.NoError(t, err)
		paths = append(paths, found...)
	}
	sort.Strings(paths)
	return paths
}

// missingReplaces returns the relay modules of the build list of a built distribution that its manifest does not
// replace.
func missingReplaces(t *testing.T, path string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = filepath.Join(filepath.Dir(path), "build")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	require.NoError(t, err)

	var missing []string
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var m struct {
			Path    string
			Main    bool
			Replace *struct{ Path string }
		}
		require.NoError(t, dec.Decode(&m))
		if !m.Main && isRelay(m.Path) && m.Replace == nil {
			missing = append(missing, m.Path)
		}
	}
	return missing
}

// TestRepositoryManifestsReplaceEveryRelayModule (Q14) pins that every manifest of this repository replaces each
// relay module of its build list by a local path, so that no build needs a release of a module.
func TestRepositoryManifestsReplaceEveryRelayModule(t *testing.T) {
	paths := manifests(t)
	require.NotEmpty(t, paths)
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			b := build(t, path)
			require.NoError(t, b.err, b.out)
			require.Empty(t, missingReplaces(t, path), "relay modules in the build list that the manifest does not replace")
		})
	}
}

// TestManifestMissingAReplaceIsReported pins that the Builder names the relay module a manifest forgets to replace,
// and says what to add.
func TestManifestMissingAReplaceIsReported(t *testing.T) {
	path := filepath.Join(root(t), "test", "distributions", "fixtures", "missing-replace", "manifest.yaml")
	b := build(t, path)

	require.Error(t, b.err, "a manifest that forgets a replace does not build")
	require.Contains(t, b.out, fmt.Sprintf("%sservice/transport", relay))
	require.Contains(t, b.out, "replaces")
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestBuildExampleDistribution (NFR-010) builds the example distribution, which brings its own command line, starts it
// with a file source, waits for readiness and resolves a flag through the example endpoint.
func TestBuildExampleDistribution(t *testing.T) {
	path := filepath.Join(root(t), "test", "distributions", "example", "manifest.yaml")
	b := build(t, path)
	require.NoError(t, b.err, b.out)
	bin := binary(t, path)

	version, err := exec.Command(bin, "version").Output()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(version), "example 0.0.0 "), "the distribution's own command line answers: %s", version)

	flags := filepath.Join(t.TempDir(), "flags.json")
	require.NoError(t, os.WriteFile(flags, []byte(`{"flags":{"greeting":{"state":"ENABLED","defaultVariant":"hello",
		"variants":{"hello":"hello","bye":"bye"}}}}`), 0o600))
	port, management := freePort(t), freePort(t)

	cmd := exec.Command(bin, "start", "--uri", flags, "--port", fmt.Sprint(port), "--management-port", fmt.Sprint(management))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
		}
	})

	ready := fmt.Sprintf("http://127.0.0.1:%d/readyz", management)
	require.Eventually(t, func() bool {
		resp, err := http.Get(ready) //nolint:noctx // a local probe
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 30*time.Second, 200*time.Millisecond, "the distribution becomes ready: %s", &stderr)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/v0/flags/greeting", port)) //nolint:noctx // a local request
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	require.Contains(t, string(body), "hello", "the example endpoint resolves the flag through the router")
}

// TestFlagdFullServesMetrics pins the telemetry of the flagd-full the Builder makes: flagd serves /metrics on the
// management port through its OpenTelemetry provider. A build that lost the provider answers 404 there, with every
// other check green.
func TestFlagdFullServesMetrics(t *testing.T) {
	path := filepath.Join(root(t), "distributions", "flagd-full", "manifest.yaml")
	b := build(t, path)
	require.NoError(t, b.err, b.out)

	flags := filepath.Join(t.TempDir(), "flags.json")
	require.NoError(t, os.WriteFile(flags, []byte(`{"flags":{"f":{"state":"ENABLED","variants":{"on":true},"defaultVariant":"on"}}}`), 0o600))
	ports := map[string]string{}
	for _, name := range []string{"port", "management-port", "sync-port", "ofrep-port"} {
		ports[name] = fmt.Sprint(freePort(t))
	}
	cmd := exec.Command(binary(t, path), "start", "--uri", "file:"+flags,
		"--port", ports["port"], "--management-port", ports["management-port"],
		"--sync-port", ports["sync-port"], "--ofrep-port", ports["ofrep-port"])
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
		}
	})

	base := "http://127.0.0.1:" + ports["management-port"]
	require.Eventually(t, func() bool {
		resp, err := http.Get(base + "/readyz") //nolint:noctx // a local probe
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 60*time.Second, 200*time.Millisecond, "flagd-full becomes ready: %s", &stderr)

	resp, err := http.Get(base + "/metrics") //nolint:noctx // a local request
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "/metrics is served by the telemetry provider")
	require.Contains(t, string(body), "# HELP", "in the Prometheus exposition format")
}

// TestComponentsMatchesManifest (UC-011) pins that the components a built distribution lists are the components its
// manifest selects: the module of every selected component is listed, and no module is listed that the manifest does
// not select. A distribution with a command line of its own has no components command.
func TestComponentsMatchesManifest(t *testing.T) {
	for _, path := range manifests(t) {
		m := readManifest(t, path)
		name := filepath.Base(filepath.Dir(path))
		if m.Cmd != nil || name == "missing-replace" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			b := build(t, path)
			require.NoError(t, b.err, b.out)

			want := map[string]bool{}
			for _, module := range m.selected() {
				want[module] = true
			}
			if m.Runtime != nil {
				delete(want, m.Runtime.Module)
			}

			out, err := exec.Command(binary(t, path), "components", "--output", "json").Output()
			require.NoError(t, err)
			var listing struct {
				Components map[string][]struct {
					Module string `json:"module"`
				} `json:"components"`
			}
			require.NoError(t, json.Unmarshal(out, &listing))
			got := map[string]bool{}
			for _, group := range listing.Components {
				for _, c := range group {
					got[c.Module] = true
				}
			}
			require.Equal(t, want, got)
		})
	}
}
