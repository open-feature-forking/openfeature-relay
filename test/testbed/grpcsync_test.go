package testbed

// UC-023 (Q14): flagd-full with a grpc source against the relay testbed, over a real connection. The selector header and
// keepalive enforcement stay unit-tested: the testbed's endpoint runs on net/http, which neither echoes headers nor
// enforces a ping policy.

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The envoy of the test-harness compose file, pinned by the same digest. Its permission-denied-listener answers every
// gRPC call with HTTP 403, which a gRPC client reads as PERMISSION_DENIED.
const (
	envoyImage = "envoyproxy/envoy:v1.40-latest@sha256:459f2a6404c340fe6afccd69d85527012d47f3c036765493e6f8f3113fa384de"

	portPermissionDenied = 9212

	envoyPermissionDenied = `static_resources:
  listeners:
    - name: permission-denied-listener
      address:
        socket_address: {address: 0.0.0.0, port_value: 9212}
      filter_chains:
        - filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                stat_prefix: permission_denied
                http_filters:
                  - name: envoy.filters.http.router
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
                route_config:
                  name: permission_denied_route
                  virtual_hosts:
                    - name: permission-denied
                      domains: ["*"]
                      routes:
                        - match: {prefix: "/", grpc: {}}
                          direct_response:
                            status: 403
                            body: {inline_string: PERMISSION_DENIED}
`
)

// relayBed starts the relay testbed, or skips when the environment names no flagd-full image to test against.
func relayBed(t *testing.T) (*bed, string) {
	t.Helper()
	image := os.Getenv("TESTBED_FLAGD_IMAGE")
	if image == "" {
		t.Skip("the grpc source test needs a flagd-full image; make testbed-grpc-sync sets TESTBED_FLAGD_IMAGE")
	}
	if kind(t) != kindRelay {
		t.Skip("the grpc source test runs against the relay testbed: TESTBED_KIND=relay")
	}
	return startBed(t), image
}

// node is a flagd-full container.
type node struct {
	t  *testing.T
	id string
	ip string
}

// startFlagd runs flagd-full with one grpc source and the given grpc settings (a JSON object body, without braces).
func startFlagd(t *testing.T, image, uri, settings string, extra ...string) *node {
	t.Helper()
	source := fmt.Sprintf(`[{"uri":%q,"provider":"grpc","grpc":{%s}}]`, uri, settings)
	id := dockerRun(t, append([]string{"run", "-d", image, "start", "--sources", source}, extra...)...)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	ip := docker(t, "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", id)
	require.NotEmpty(t, ip, "the container has no address")
	return &node{t: t, id: id, ip: ip}
}

// dockerRun runs `docker run -d` and returns the container id from stdout alone, whatever else docker prints.
func dockerRun(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("docker", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	require.NoError(t, err, "docker %s: %s", strings.Join(args, " "), stderr.String())
	return strings.TrimSpace(string(out))
}

func containerIP(t *testing.T, id string) string {
	t.Helper()
	ip := docker(t, "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", id)
	require.NotEmpty(t, ip, "the container has no address")
	return ip
}

// logLine is one line of the container's output with the time docker recorded for it.
type logLine struct {
	at   time.Time
	text string
}

func (n *node) logs() []logLine {
	n.t.Helper()
	out, _ := exec.Command("docker", "logs", "--timestamps", n.id).CombinedOutput()
	var lines []logLine
	for _, l := range strings.Split(string(out), "\n") {
		stamp, text, found := strings.Cut(l, " ")
		if !found {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			continue
		}
		lines = append(lines, logLine{at: at, text: text})
	}
	return lines
}

// find returns the lines that contain the text, in order.
func (n *node) find(text string) []logLine {
	var hits []logLine
	for _, l := range n.logs() {
		if strings.Contains(l.text, text) {
			hits = append(hits, l)
		}
	}
	return hits
}

func (n *node) text() string {
	var b strings.Builder
	for _, l := range n.logs() {
		b.WriteString(l.text + "\n")
	}
	return b.String()
}

func (n *node) ready() bool {
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + net.JoinHostPort(n.ip, fmt.Sprint(portManagement)) + "/readyz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (n *node) requireReady() {
	n.t.Helper()
	require.Eventually(n.t, n.ready, 60*time.Second, 100*time.Millisecond, "flagd-full becomes ready; logs:\n%s", n.text())
}

// resolves reports whether flagd-full evaluates a flag of the testbed's default configuration, which it got over sync.
func (n *node) resolves() bool {
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Post(
		"http://"+net.JoinHostPort(n.ip, fmt.Sprint(portEvaluation))+evaluationPath+"ResolveBoolean",
		"application/json", strings.NewReader(`{"flagKey":"boolean-flag","context":{}}`))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// exited waits for the container to stop and returns its exit code.
func (n *node) exited(within time.Duration) (int, bool) {
	n.t.Helper()
	done := make(chan string, 1)
	go func() {
		out, _ := exec.Command("docker", "wait", n.id).Output()
		done <- strings.TrimSpace(string(out))
	}()
	select {
	case out := <-done:
		code, err := strconv.Atoi(out)
		require.NoError(n.t, err, "docker wait: %q", out)
		return code, true
	case <-time.After(within):
		return 0, false
	}
}

// TestGRPCSourceReconnects: /restart closes the testbed's listeners for real, and flagd-full waits retryBackoffMaxMs
// between the end of the stream and the next attempt (Q4: the delay is a lower bound, because the layers add up).
func TestGRPCSourceReconnects(t *testing.T) {
	b, image := relayBed(t)
	const backoffMax = 2 * time.Second
	n := startFlagd(t, image, b.addr(portSync), `"retryBackoffMs":500,"retryBackoffMaxMs":2000`)
	n.requireReady()
	require.True(t, n.resolves(), "the flags came over sync; logs:\n%s", n.text())

	require.Equal(t, 200, b.call(http.MethodGet, "/restart?seconds=3").code)
	b.requireRefused(portSync)

	var ended, reopened time.Time
	require.Eventually(t, func() bool {
		stream := n.find("error with stream listener")
		again := n.find("connection re-established")
		if len(stream) == 0 || len(again) == 0 {
			return false
		}
		ended, reopened = stream[0].at, again[0].at
		return true
	}, 60*time.Second, 200*time.Millisecond, "the stream ends and is re-established; logs:\n%s", n.text())

	require.GreaterOrEqual(t, reopened.Sub(ended), backoffMax, "the stream was re-opened before retryBackoffMaxMs")
	require.Less(t, reopened.Sub(ended), 30*time.Second, "the stream was not re-opened in reasonable time")
	b.waitReady(30 * time.Second)
	require.True(t, n.ready(), "flagd-full stays ready; logs:\n%s", n.text())
	require.True(t, n.resolves(), "flagd-full still evaluates")
}

// TestGRPCSourceFatalCode: the permission-denied listener of envoy refuses the first call, and a listed fatal code ends
// flagd (Q5). Without the setting the same upstream is retried and flagd keeps running.
func TestGRPCSourceFatalCode(t *testing.T) {
	image := os.Getenv("TESTBED_FLAGD_IMAGE")
	if image == "" {
		t.Skip("the grpc source test needs a flagd-full image; make testbed-grpc-sync sets TESTBED_FLAGD_IMAGE")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "envoy.yaml"), []byte(envoyPermissionDenied), 0o644))
	require.NoError(t, os.Chmod(dir, 0o755))
	// the pull is separate: `docker run` on a missing image prints its progress next to the id
	docker(t, "pull", "--quiet", envoyImage)
	envoy := dockerRun(t, "run", "-d", "-v", dir+":/etc/envoy-test:ro", envoyImage, "-c", "/etc/envoy-test/envoy.yaml")
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", envoy).Run() })
	upstream := net.JoinHostPort(containerIP(t, envoy), fmt.Sprint(portPermissionDenied))
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", upstream, time.Second)
		if err == nil {
			conn.Close()
		}
		return err == nil
	}, 60*time.Second, 200*time.Millisecond, "envoy listens on 9212; logs:\n%s", docker(t, "logs", envoy))

	t.Run("a listed code ends flagd", func(t *testing.T) {
		n := startFlagd(t, image, upstream, `"fatalStatusCodes":["PERMISSION_DENIED"]`)
		code, ok := n.exited(60 * time.Second)
		require.True(t, ok, "flagd-full did not exit; logs:\n%s", n.text())
		require.NotZero(t, code, "flagd-full exited cleanly; logs:\n%s", n.text())
		out := n.text()
		require.Contains(t, out, upstream, "the exit names the source")
		require.Contains(t, out, "PERMISSION_DENIED", "the exit names the code; logs:\n%s", out)
	})

	t.Run("without the setting flagd keeps retrying", func(t *testing.T) {
		n := startFlagd(t, image, upstream, `"retryBackoffMaxMs":1000`)
		_, exited := n.exited(8 * time.Second)
		require.False(t, exited, "flagd-full exited; logs:\n%s", n.text())
		require.False(t, n.ready(), "a source that never delivered a payload is not ready")
	})
}

// TestGRPCSourceStreamDeadline: with streamDeadlineMs set, the stream is re-opened at the deadline, with no error and
// no delay (Q7).
func TestGRPCSourceStreamDeadline(t *testing.T) {
	b, image := relayBed(t)
	const deadline = 2 * time.Second
	n := startFlagd(t, image, b.addr(portSync), `"streamDeadlineMs":2000,"retryBackoffMaxMs":5000`, "--debug")
	n.requireReady()

	require.Eventually(t, func() bool { return len(n.find("reached its deadline")) >= 3 }, 30*time.Second, 200*time.Millisecond,
		"the stream is recycled three times; logs:\n%s", n.text())

	recycled := n.find("reached its deadline")
	for i := 1; i < len(recycled); i++ {
		gap := recycled[i].at.Sub(recycled[i-1].at)
		// a recycle that waited retryBackoffMaxMs would be 7 s apart
		require.InDelta(t, deadline.Seconds(), gap.Seconds(), 1.5, "recycle %d came %s after the one before", i, gap)
	}
	require.Empty(t, n.find("error with stream listener"), "a recycle is not an error; logs:\n%s", n.text())
	require.True(t, n.ready())
	require.True(t, n.resolves())
}
