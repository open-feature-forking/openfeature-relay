package cmd

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	evaluator "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	flagevaluation "github.com/open-feature-forking/openfeature-relay/service/flag-evaluation"
	flagsync "github.com/open-feature-forking/openfeature-relay/service/flag-sync"
	syncfile "github.com/open-feature-forking/openfeature-relay/sync/file"
	"github.com/open-feature-forking/openfeature-relay/testbed/control"
	"github.com/open-feature-forking/openfeature-relay/testbed/source"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// bed is a testbed in a temporary directory laid out as the image is: rawflags/, configs/ and flags/ under the working
// directory, the control API on a free port, and the flagd ports on free ports.
type bed struct {
	t       *testing.T
	dir     string
	target  *target
	server  *control.Server
	ports   struct{ rpc, management, sync int }
	caPool  *x509.CertPool
	control string
}

// issuedPorts are the ports freePort has handed out in this process. A port is free again the moment it is closed, so
// the next call can get the same one, and one start would listen twice on it.
var issuedPorts sync.Map

func freePort(t *testing.T) int {
	t.Helper()
	// a port the kernel hands out again stays open until a new one comes, so the next listen cannot get it
	var held []net.Listener
	defer func() {
		for _, h := range held {
			_ = h.Close()
		}
	}()
	for {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := l.Addr().(*net.TCPAddr).Port
		if _, taken := issuedPorts.LoadOrStore(port, true); taken {
			held = append(held, l)
			continue
		}
		_ = l.Close()
		return int(port)
	}
}

func copyDir(t *testing.T, from, to string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(from, "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, files, "the test harness is missing: git submodule update --init test-harness")
	require.NoError(t, os.MkdirAll(to, 0o755))
	for _, f := range files {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(to, filepath.Base(f)), data, 0o600))
	}
}

// writeCertificate writes a self-signed server certificate for localhost and returns a pool that trusts it.
func writeCertificate(t *testing.T, dir string) *x509.CertPool {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "server-cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "server-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return pool
}

// newBed starts a testbed on the configurations of the test harness, with the ssl configuration pointed at a
// certificate of the test, and with the extra configurations written as given.
func newBed(t *testing.T, startTimeout time.Duration, extra map[string]string) *bed {
	t.Helper()
	b := &bed{t: t, dir: t.TempDir()}
	copyDir(t, "../../test-harness/flags", filepath.Join(b.dir, "rawflags"))
	copyDir(t, "../../test-harness/launchpad/configs", filepath.Join(b.dir, "configs"))
	require.NoError(t, os.MkdirAll(filepath.Join(b.dir, "flags"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(b.dir, "ssl"), 0o755))
	b.caPool = writeCertificate(t, filepath.Join(b.dir, "ssl"))
	sslPath := filepath.Join(b.dir, "configs", "ssl.json")
	ssl, err := os.ReadFile(sslPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(sslPath, []byte(strings.ReplaceAll(string(ssl), "/ssl/", filepath.ToSlash(b.dir)+"/ssl/")), 0o600))
	for name, body := range extra {
		require.NoError(t, os.WriteFile(filepath.Join(b.dir, "configs", name+".json"), []byte(body), 0o600))
	}
	t.Chdir(b.dir)

	b.ports.rpc, b.ports.management, b.ports.sync = freePort(t), freePort(t), freePort(t)
	t.Setenv("FLAGD_PORT", fmt.Sprint(b.ports.rpc))
	t.Setenv("FLAGD_MANAGEMENT_PORT", fmt.Sprint(b.ports.management))
	t.Setenv("FLAGD_SYNC_PORT", fmt.Sprint(b.ports.sync))

	src := source.New()
	var components distribution.Components
	components.Sync = append(syncfile.NewFactories(), src.Factory())
	components.ConfigurationFormats = append(components.ConfigurationFormats, flagd.NewFactory())
	components.Evaluators = append(components.Evaluators, evaluator.NewFactory())
	components.Endpoints = append(components.Endpoints, flagevaluation.NewFactory(), flagsync.NewFactory())

	cfg := control.Config{
		Address: "127.0.0.1:0", Configs: filepath.Join(b.dir, "configs"), RawFlags: filepath.Join(b.dir, "rawflags"),
		Render: filepath.Join(b.dir, "flags", "allFlags.json"), StartTimeout: startTimeout,
	}.WithDefaults()
	b.target = newTarget(cfg.Configs, "test", src, components, logger.NewLogger(zap.NewNop(), false))
	b.server = control.New(cfg, b.target)
	b.target.document = b.server.Document

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- b.server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-served:
		case <-time.After(15 * time.Second):
			t.Error("the testbed did not stop")
		}
		b.target.shutdown()
	})
	require.Eventually(t, func() bool { return b.server.Addr() != "" }, bedReadyTimeout, 10*time.Millisecond)
	b.control = "http://" + b.server.Addr()
	b.requireServing()
	return b
}

// bedReadyTimeout bounds the default start, after which the control port opens; a CI run with -race needs far more than a
// local one.
const bedReadyTimeout = 30 * time.Second

// get calls the control API.
func (b *bed) get(path string) (int, string) {
	b.t.Helper()
	resp, err := http.Get(b.control + path) //nolint:noctx // local test endpoint
	require.NoError(b.t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(b.t, err)
	return resp.StatusCode, string(body)
}

func (b *bed) mustGet(path string) {
	b.t.Helper()
	code, body := b.get(path)
	require.Equal(b.t, 200, code, body)
}

func (b *bed) client() *http.Client {
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: b.caPool, MinVersion: tls.VersionTLS12}, DisableKeepAlives: true,
	}}
}

// variant resolves changing-flag over the flag evaluation endpoint, plain or TLS.
func (b *bed) variant(scheme string) (string, error) { return b.variantWith(scheme, "") }

// variantWith resolves changing-flag under a selector.
func (b *bed) variantWith(scheme, selector string) (string, error) {
	body := bytes.NewReader([]byte(`{"flagKey":"changing-flag","context":{}}`))
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s://localhost:%d/flagd.evaluation.v1.Service/ResolveString", scheme, b.ports.rpc), body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if selector != "" {
		req.Header.Set("Flagd-Selector", selector)
	}
	resp, err := b.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var out struct {
		Variant string `json:"variant"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("%d: %s", resp.StatusCode, data)
	}
	return out.Variant, nil
}

func (b *bed) requireVariant(want string) {
	b.t.Helper()
	got, err := b.variant("http")
	require.NoError(b.t, err)
	require.Equal(b.t, want, got)
}

// refused reports whether nothing accepts connections on the port.
func refused(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return true
	}
	_ = conn.Close()
	return false
}

func (b *bed) requirePortsRefused() {
	b.t.Helper()
	for name, port := range map[string]int{"flag evaluation": b.ports.rpc, "management": b.ports.management, "flag sync": b.ports.sync} {
		require.Eventually(b.t, func() bool { return refused(port) }, 5*time.Second, 20*time.Millisecond, name)
	}
}

func (b *bed) requireServing() {
	b.t.Helper()
	require.Eventually(b.t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", b.ports.management)) //nolint:noctx // local test endpoint
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == 200
	}, 10*time.Second, 20*time.Millisecond)
}

func hashTree(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*"))
		require.NoError(t, err)
		for _, f := range files {
			info, err := os.Stat(f)
			require.NoError(t, err)
			data, err := os.ReadFile(f)
			require.NoError(t, err)
			out[f] = fmt.Sprintf("%d %s", info.ModTime().UnixNano(), data)
		}
	}
	return out
}

// TestStartReloadsCleanedStore: a start with the configuration in use resets the runtime and reloads the combined flags
// from memory, with the toggle made since, and renders the file again.
func TestStartReloadsCleanedStore(t *testing.T) {
	b := newBed(t, 10*time.Second, nil)
	b.requireVariant("foo")
	b.mustGet("/change")
	b.requireVariant("bar")
	require.NoError(t, os.Remove(filepath.Join(b.dir, "flags", "allFlags.json")))

	b.mustGet("/start?config=default")

	b.requireVariant("bar")
	rendered, err := os.ReadFile(filepath.Join(b.dir, "flags", "allFlags.json"))
	require.NoError(t, err)
	require.Contains(t, string(rendered), `"changing-flag"`)
}

// TestSameNameDoesNotRebuild: only a change of the configuration builds a runtime.
func TestSameNameDoesNotRebuild(t *testing.T) {
	b := newBed(t, 10*time.Second, nil)
	first := b.target.runtime()

	b.mustGet("/start?config=default")
	b.mustGet("/start")
	require.Same(t, first, b.target.runtime(), "the same configuration resets the runtime in use")

	b.mustGet("/start?config=sync-payload")
	require.NotSame(t, first, b.target.runtime(), "another configuration builds a runtime")
}

// TestLoadReturnsAfterAccept: when /change has answered, the runtime serves the new variant at once.
func TestLoadReturnsAfterAccept(t *testing.T) {
	b := newBed(t, 10*time.Second, nil)
	for i := 0; i < 20; i++ {
		b.mustGet("/change")
		want := map[bool]string{true: "bar", false: "foo"}[i%2 == 0]
		b.requireVariant(want)
	}
}

// TestLoadRejectedRendersNothing: a document the format refuses ends the load with the format's error and changes
// what the runtime serves.
func TestLoadRejectedRendersNothing(t *testing.T) {
	b := newBed(t, 10*time.Second, nil)
	b.requireVariant("foo")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := b.target.Load(ctx, []byte(`{"flags":{"changing-flag":{"state":"ENABLED","defaultVariant":"nope","variants":{"foo":"foo"}}}}`), "rejected-1")

	require.Error(t, err)
	b.requireVariant("foo")
}

// TestChangeThroughTrigger: /change reaches the store through the testbed source, and no file under rawflags or
// configs is touched.
func TestChangeThroughTrigger(t *testing.T) {
	b := newBed(t, 10*time.Second, nil)
	before := hashTree(t, filepath.Join(b.dir, "rawflags"), filepath.Join(b.dir, "configs"))

	b.mustGet("/change")
	b.requireVariant("bar")
	b.mustGet("/change")
	b.requireVariant("foo")

	require.Equal(t, before, hashTree(t, filepath.Join(b.dir, "rawflags"), filepath.Join(b.dir, "configs")))
}

// TestChangeSurvivesRestart: the toggle is part of the in-memory state, so a restart keeps it.
func TestChangeSurvivesRestart(t *testing.T) {
	b := newBed(t, 10*time.Second, nil)
	b.mustGet("/change")

	b.mustGet("/restart?seconds=0")
	b.requireServing()

	require.Eventually(t, func() bool { v, err := b.variant("http"); return err == nil && v == "bar" }, 10*time.Second, 50*time.Millisecond)
}

// TestChangeRestartChange: the sequence that the first revision of the plan could not do, /change, a restart, /change
// again: both toggles reach the provider.
func TestChangeRestartChange(t *testing.T) {
	b := newBed(t, 10*time.Second, nil)
	b.mustGet("/change")
	b.requireVariant("bar")
	b.mustGet("/restart?seconds=1")
	b.requirePortsRefused()
	b.requireServing()
	require.Eventually(t, func() bool { v, err := b.variant("http"); return err == nil && v == "bar" }, 10*time.Second, 50*time.Millisecond)

	b.mustGet("/change")

	b.requireVariant("foo")
}

// TestRestartClosesEveryRuntimePort: /stop and /restart refuse every port of the runtime, and /restart brings them
// back.
func TestRestartClosesEveryRuntimePort(t *testing.T) {
	b := newBed(t, 10*time.Second, nil)

	b.mustGet("/stop")
	b.requirePortsRefused()

	b.mustGet("/restart?seconds=1")
	b.requirePortsRefused()
	b.requireServing()
}

// TestControlPortOutsideGate: the control API answers while the runtime's ports are closed.
func TestControlPortOutsideGate(t *testing.T) {
	b := newBed(t, 10*time.Second, nil)
	b.mustGet("/stop")
	b.requirePortsRefused()

	b.mustGet("/change")
	code, _ := b.get("/restart?seconds=x")
	require.Equal(t, 400, code)
	b.mustGet("/start")
	b.requireServing()
}

// TestConfigSwitch: every configuration of the test harness starts in the same process, ssl serves TLS on the flag
// evaluation port, the toggle survives, the old runtime has returned from Run before the new one listens, and the
// round trip default, ssl, default stays under 10 seconds.
func TestConfigSwitch(t *testing.T) {
	b := newBed(t, 10*time.Second, nil)
	b.mustGet("/change")
	b.requireVariant("bar")

	var oldDone atomic.Pointer[chan error]
	var listensBeforeOldReturned atomic.Int32
	b.target.onListen = func() {
		if done := oldDone.Load(); done != nil {
			select {
			case <-*done:
			default:
				listensBeforeOldReturned.Add(1)
			}
		}
	}
	switchTo := func(name string) {
		b.target.mu.Lock()
		done := b.target.done
		b.target.mu.Unlock()
		oldDone.Store(&done)
		b.mustGet("/start?config=" + name)
	}

	began := time.Now()
	switchTo("ssl")
	got, err := b.variant("https")
	require.NoError(t, err)
	require.Equal(t, "bar", got, "the toggle survives a switch, and ssl serves TLS under the certificate of the test")
	_, err = b.variant("http")
	require.Error(t, err, "plain HTTP is refused under ssl")
	switchTo("default")
	require.Less(t, time.Since(began), 10*time.Second, "default to ssl and back")
	b.requireVariant("bar")

	switchTo("sync-payload")
	b.requireServing()
	b.requireVariant("bar")
	switchTo("metadata")
	b.requireServing()
	switchTo("default")
	b.requireVariant("bar")

	require.Zero(t, listensBeforeOldReturned.Load(), "a rebuild waits for the old runtime to return from Run")
}

// brokenConfig names a source that never delivers, so a switch to it does not get ready.
const brokenConfig = `{"sources":[{"uri":"rawflags/missing.json","provider":"file"}]}`

// TestChangeAfterFailedSwitch: after a switch that failed there is no runtime, so /change fails at once, and the next
// start of the last good configuration builds one again.
func TestChangeAfterFailedSwitch(t *testing.T) {
	b := newBed(t, 2*time.Second, map[string]string{"broken": brokenConfig})

	code, body := b.get("/start?config=broken")
	require.Equal(t, 500, code, body)
	b.requirePortsRefused()

	began := time.Now()
	code, _ = b.get("/change")
	require.Equal(t, 500, code)
	require.Less(t, time.Since(began), time.Second, "no runtime: the change fails at once and does not wait")

	b.mustGet("/start")
	b.requireServing()
	b.requireVariant("foo")
}

// TestRuntimeNeverReadsRenderedFile: no source of any configuration of the test harness reads the rendered file; the
// one that named it is the testbed source.
func TestRuntimeNeverReadsRenderedFile(t *testing.T) {
	b := newBed(t, 10*time.Second, nil)
	configs, err := filepath.Glob(filepath.Join(b.dir, "configs", "*.json"))
	require.NoError(t, err)
	require.Len(t, configs, 4)
	for _, path := range configs {
		sources, err := loadForTest(b, path)
		require.NoError(t, err, path)
		for _, s := range sources {
			if s.URI == combinedFile {
				require.Equal(t, "testbed", s.Provider, "%s reads the rendered file", filepath.Base(path))
			}
		}
	}
}

// TestSelectorAddressesTheCombinedSource: the source of the combined flags keeps its URI, so a selector that names it
// resolves to the testbed source's flags, and a selector that names another source does not.
func TestSelectorAddressesTheCombinedSource(t *testing.T) {
	b := newBed(t, 10*time.Second, nil)

	got, err := b.variantWith("http", "source=flags/allFlags.json")
	require.NoError(t, err)
	require.Equal(t, "foo", got)

	_, err = b.variantWith("http", "source=rawflags/selector-flags.json")
	require.Error(t, err, "the other source does not hold changing-flag")
}

func TestUnknownConfigurationLeavesTheRuntimeServing(t *testing.T) {
	b := newBed(t, 10*time.Second, nil)
	began := time.Now()

	code, body := b.get("/start?config=nope")

	require.Equal(t, 500, code)
	require.Contains(t, body, `unknown configuration \"nope\"`)
	require.Less(t, time.Since(began), time.Second)
	b.requireVariant("foo")
}

// stuckConfig names the combined flags and a file whose flags the format never accepts, so a switch to it does not get
// ready and times out instead of failing at once.
const stuckConfig = `{"sources":[{"uri":"flags/allFlags.json","provider":"file"},{"uri":"configs/never.json","provider":"file"}]}`

// TestChangeAfterTimedOutSwitch: a switch that times out leaves no runtime behind, so /change fails at once and does not
// load into it.
func TestChangeAfterTimedOutSwitch(t *testing.T) {
	b := newBed(t, 2*time.Second, map[string]string{"stuck": stuckConfig, "never": `{"flags": 5}`})

	code, body := b.get("/start?config=stuck")
	require.Equal(t, 500, code, body)
	require.Contains(t, body, "health check timed out")
	require.Nil(t, b.target.runtime(), "the runtime of the timed out switch is gone")
	b.requirePortsRefused()

	began := time.Now()
	code, _ = b.get("/change")
	require.Equal(t, 500, code)
	require.Less(t, time.Since(began), time.Second)

	b.mustGet("/start?config=default")
	b.requireServing()
}

// TestCrossFlagSetFlagByTestbedSource: the scenario "flag disabled in one flag set, enabled in another": the selector
// flags/allFlags.json reaches the testbed source's copy of the flag, which is disabled, and the other selector reaches
// the enabled one.
func TestCrossFlagSetFlagByTestbedSource(t *testing.T) {
	b := newBed(t, 10*time.Second, nil)
	resolve := func(selector string) string {
		req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://localhost:%d/flagd.evaluation.v1.Service/ResolveBoolean", b.ports.rpc),
			strings.NewReader(`{"flagKey":"cross-flagset-flag","context":{}}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Flagd-Selector", selector)
		resp, err := b.client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return string(body)
	}

	require.Contains(t, resolve("flags/allFlags.json"), "DISABLED")
	require.Contains(t, resolve("rawflags/selector-flags.json"), "STATIC")
}
