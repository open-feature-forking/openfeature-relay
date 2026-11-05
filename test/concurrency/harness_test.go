package concurrency

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Timing of the schedule. D is the apply bound; eps is clock slack. See the PR description for the measurement of D.
var (
	period = envMillis("RELAY_LOAD_P", 4100*time.Millisecond)
	bound  = envMillis("RELAY_LOAD_D", 3100*time.Millisecond)
)

const eps = 100 * time.Millisecond

// envMillis lets a measurement run try other values; the defaults are the ones the PR records.
func envMillis(name string, def time.Duration) time.Duration {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return time.Duration(v) * time.Millisecond
	}
	return def
}

var flagdBinary string

func TestMain(m *testing.M) {
	code := func() int {
		if os.Getenv("RELAY_LOAD") == "" {
			return m.Run()
		}
		dir, err := os.MkdirTemp("", "relay-load-build-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer os.RemoveAll(dir)
		if flagdBinary, err = buildRace(dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return m.Run()
	}()
	os.Exit(code)
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// buildRace builds flagd-minimal with the Builder, as test/distributions does, with the race detector on. The Builder
// keeps GOFLAGS and a set CGO_ENABLED.
func buildRace(dir string) (string, error) {
	root := repoRoot()
	cmd := exec.Command("go", "run", "./cmd/relay-builder",
		"--manifest", filepath.Join(root, "distributions", "flagd-minimal", "manifest.yaml"), "--output", dir)
	cmd.Dir = filepath.Join(root, "builder")
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=1", "GOFLAGS=-race")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building flagd-minimal: %w\n%s", err, out)
	}
	binary := filepath.Join(dir, "flagd")
	out, err := exec.Command("go", "version", "-m", binary).Output()
	if err != nil || !strings.Contains(string(out), "-race=true") {
		return "", fmt.Errorf("flagd-minimal was not built with the race detector: %v\n%s", err, out)
	}
	return binary, nil
}

// requireLoad skips a test unless RELAY_LOAD is set and Docker shares this machine's network and clock. In CI (CI is
// set) a load run that cannot happen fails instead: a skip would read as a green job.
func requireLoad(t *testing.T) {
	t.Helper()
	if os.Getenv("RELAY_LOAD") == "" {
		t.Skip("RELAY_LOAD is not set")
	}
	out, err := exec.Command("docker", "info", "--format", "{{.OperatingSystem}}").Output()
	if err != nil {
		skipUnlessCI(t, "docker is not available: %v", err)
	}
	if os := strings.TrimSpace(string(out)); strings.Contains(os, "Docker Desktop") {
		skipUnlessCI(t, "%s runs containers in a VM: --network host does not reach this machine and the clock is the VM's", os)
	}
}

func skipUnlessCI(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

// k6ImageName is the Makefile's K6_IMAGE, pinned by digest; images are pulled, never pushed (K2).
func k6ImageName(t *testing.T) string {
	t.Helper()
	image := os.Getenv("K6_IMAGE")
	require.NotEmpty(t, image, "K6_IMAGE is the Makefile's; run through make test-load-smoke or make test-load")
	return image
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

type flagdProc struct {
	Dir                                 string
	Flags                               string
	EvalPort, OFREPPort, SyncPort, Mgmt int
	stderr                              *syncBuffer
	cmd                                 *exec.Cmd
	done                                chan error
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// startFlagd writes generation 0 and the protoset into a native temporary directory, starts the race build of
// flagd-minimal on free ports and waits for readiness.
func startFlagd(t *testing.T) *flagdProc {
	t.Helper()
	require.NotEmpty(t, flagdBinary, "TestMain builds flagd-minimal when RELAY_LOAD is set")
	dir := t.TempDir()
	p := &flagdProc{
		Dir: dir, Flags: filepath.Join(dir, "flags.json"),
		EvalPort: freePort(t), OFREPPort: freePort(t), SyncPort: freePort(t), Mgmt: freePort(t),
		stderr: &syncBuffer{}, done: make(chan error, 1),
	}
	require.NoError(t, WriteAtomic(p.Flags, Document(0)))
	set, err := Protoset()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "protoset.bin"), set, 0o644))

	p.cmd = exec.Command(flagdBinary, "start", "--uri", "file:"+p.Flags,
		"--port", fmt.Sprint(p.EvalPort), "--ofrep-port", fmt.Sprint(p.OFREPPort),
		"--sync-port", fmt.Sprint(p.SyncPort), "--management-port", fmt.Sprint(p.Mgmt))
	p.cmd.Env = append(os.Environ(), "GORACE=halt_on_error=1")
	if os.Getenv("RELAY_LOAD_FSNOTIFY") != "" {
		// Inside Kubernetes the file source watches with fsnotify; elsewhere it polls the file's status every second.
		p.cmd.Env = append(p.cmd.Env, "KUBERNETES_SERVICE_HOST=127.0.0.1")
	}
	p.cmd.Stderr = p.stderr
	require.NoError(t, p.cmd.Start())
	go func() { p.done <- p.cmd.Wait() }()
	t.Cleanup(func() { _ = p.stop(t) }) // the tests that care about the exit read it from stop themselves

	require.Eventually(t, func() bool {
		resp, err := http.Get(p.ReadyURL()) //nolint:noctx // a local probe
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 60*time.Second, 200*time.Millisecond, "flagd-minimal becomes ready:\n%s", p.stderr)
	return p
}

func (p *flagdProc) ReadyURL() string { return fmt.Sprintf("http://127.0.0.1:%d/readyz", p.Mgmt) }

func (p *flagdProc) SyncAddr() string { return fmt.Sprintf("127.0.0.1:%d", p.SyncPort) }

// stop sends SIGTERM and returns flagd's exit error, nil for a clean exit. It is safe to call twice.
func (p *flagdProc) stop(t *testing.T) error {
	t.Helper()
	select {
	case err := <-p.done:
		p.done <- err
		return err
	default:
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-p.done:
		p.done <- err
		return err
	case <-time.After(30 * time.Second):
		_ = p.cmd.Process.Kill()
		return fmt.Errorf("flagd did not exit within 30s of SIGTERM")
	}
}

// k6Run is the result of one k6 container run.
type k6Run struct {
	ExitCode int
	Output   string
}

// runK6 runs a script from k6/ in a container on the host network, with flagd's ports and the protoset when p is given.
func runK6(ctx context.Context, t *testing.T, p *flagdProc, script string, env map[string]string) k6Run {
	t.Helper()
	suite, err := filepath.Abs(".")
	require.NoError(t, err)
	args := []string{"run", "--rm", "--network", "host", "-v", suite + ":/suite:ro"}
	if p != nil {
		args = append(args, "-v", p.Dir+":/work:ro",
			"-e", "HOST=127.0.0.1", "-e", fmt.Sprintf("EVAL_PORT=%d", p.EvalPort),
			"-e", fmt.Sprintf("OFREP_PORT=%d", p.OFREPPort), "-e", fmt.Sprintf("SYNC_PORT=%d", p.SyncPort),
			"-e", "PROTOSET=/work/protoset.bin")
	}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, k6ImageName(t), "run", "--quiet", "--log-format", "raw", "/suite/k6/"+script)
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	res := k6Run{Output: out.String()}
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		require.True(t, ok, "docker run: %v\n%s", err, res.Output)
		res.ExitCode = ee.ExitCode()
	}
	return res
}
