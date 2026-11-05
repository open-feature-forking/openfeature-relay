// Package testbed pins the contract of a flagd testbed image (UC-022): the launchpad's control port on 8080 and what it
// does to flagd's ports, as the provider test suites rely on it. The image under test is named by TESTBED_IMAGE, and
// TESTBED_KIND says whether it is the upstream launchpad image ("launchpad", the default) or the relay testbed
// ("relay"). The cases marked launchpad-only pin quirks the relay testbed fixes on purpose, so they run on the
// launchpad image only.
package testbed

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	kindLaunchpad = "launchpad"
	kindRelay     = "relay"

	portEvaluation = 8013
	portManagement = 8014
	portSync       = 8015
	portControl    = 8080

	evaluationPath = "/flagd.evaluation.v1.Service/"
	syncPath       = "/flagd.sync.v1.FlagSyncService/SyncFlags"
)

func kind(t *testing.T) string {
	t.Helper()
	switch k := os.Getenv("TESTBED_KIND"); k {
	case "", kindLaunchpad:
		return kindLaunchpad
	case kindRelay:
		return kindRelay
	default:
		t.Fatalf("TESTBED_KIND is %q, want %q or %q", k, kindLaunchpad, kindRelay)
		return ""
	}
}

// bed is a running testbed container.
type bed struct {
	t     *testing.T
	id    string
	ip    string
	flags string // the host directory mounted as /flags
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	require.NoError(t, err, "docker %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// startBed runs the image as the compose file of the testbed does: /flags is a host directory, and flagd starts with
// the default configuration. It returns once flagd is ready.
func startBed(t *testing.T) *bed {
	t.Helper()
	image := os.Getenv("TESTBED_IMAGE")
	if image == "" {
		t.Skip("the testbed contract needs a testbed image; make testbed-contract IMAGE=<image> sets TESTBED_IMAGE")
	}
	flags := t.TempDir()
	id := docker(t, "run", "-d", "-v", flags+":/flags", image)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	ip := docker(t, "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", id)
	require.NotEmpty(t, ip, "the container has no address")
	b := &bed{t: t, id: id, ip: ip, flags: flags}
	b.waitReady(60 * time.Second)
	// the launchpad serves its control port only after flagd is ready
	require.Eventually(t, func() bool { return !b.refused(portControl) }, 30*time.Second, 100*time.Millisecond, "the control port opens")
	return b
}

func (b *bed) addr(port int) string { return net.JoinHostPort(b.ip, fmt.Sprint(port)) }

type reply struct {
	code  int
	ctype string
	body  string
}

// call sends one request to the control port. The launchpad answers any HTTP method.
func (b *bed) call(method, path string) reply {
	b.t.Helper()
	client := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	req, err := http.NewRequest(method, "http://"+b.addr(portControl)+path, nil)
	require.NoError(b.t, err)
	resp, err := client.Do(req)
	require.NoError(b.t, err, "%s %s", method, path)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(b.t, err)
	return reply{code: resp.StatusCode, ctype: resp.Header.Get("Content-Type"), body: string(body)}
}

func ok(message string) string { return `{"status":"success","message":"` + message + "\"}\n" }

func (b *bed) ready() bool {
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + b.addr(portManagement) + "/readyz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (b *bed) waitReady(within time.Duration) {
	b.t.Helper()
	require.Eventually(b.t, b.ready, within, 100*time.Millisecond, "flagd becomes ready")
}

// refused reports whether nothing listens on the port: the connection is refused, not accepted and closed.
func (b *bed) refused(port int) bool {
	conn, err := net.DialTimeout("tcp", b.addr(port), time.Second)
	if err == nil {
		conn.Close()
		return false
	}
	return errors.Is(err, syscall.ECONNREFUSED)
}

func (b *bed) requireRefused(ports ...int) {
	b.t.Helper()
	for _, port := range ports {
		require.Eventually(b.t, func() bool { return b.refused(port) }, 3*time.Second, 50*time.Millisecond, "port %d refuses connections", port)
	}
}

// requireNeverRefused polls the port for the duration and fails the first time a connection is refused.
func (b *bed) requireNeverRefused(port int, d time.Duration) {
	b.t.Helper()
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		require.False(b.t, b.refused(port), "port %d refused a connection, so flagd was stopped by a restart that should have been cancelled", port)
	}
}

// requireStaysRefused polls the port for the duration and fails the first time a connection is accepted.
func (b *bed) requireStaysRefused(port int, d time.Duration) {
	b.t.Helper()
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		require.True(b.t, b.refused(port), "port %d accepted a connection, so a pending restart was not cancelled", port)
	}
}

func (b *bed) rawFile(path string) string {
	b.t.Helper()
	return docker(b.t, "exec", b.id, "cat", path)
}

func (b *bed) allFlags() ([]byte, error) { return os.ReadFile(filepath.Join(b.flags, "allFlags.json")) }

// httpClient talks to flagd's connect endpoints, over TLS with the testbed's custom root CA when tlsCA is set.
func (b *bed) httpClient(tlsCA []byte) (*http.Client, string) {
	b.t.Helper()
	transport := &http.Transport{DisableKeepAlives: true}
	scheme := "http"
	if tlsCA != nil {
		pool := x509.NewCertPool()
		require.True(b.t, pool.AppendCertsFromPEM(tlsCA))
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12}
		scheme = "https"
	}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second}, scheme
}

// resolveString evaluates a string flag over flagd's connect endpoint on 8013.
func (b *bed) resolveString(tlsCA []byte, flag string) (value string, code int) {
	b.t.Helper()
	client, scheme := b.httpClient(tlsCA)
	req, err := http.NewRequest(http.MethodPost, scheme+"://"+b.addr(portEvaluation)+evaluationPath+"ResolveString",
		strings.NewReader(fmt.Sprintf(`{"flagKey":%q,"context":{}}`, flag)))
	require.NoError(b.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	resp, err := client.Do(req)
	require.NoError(b.t, err)
	defer resp.Body.Close()
	var out struct {
		Value string `json:"value"`
	}
	data, err := io.ReadAll(resp.Body)
	require.NoError(b.t, err)
	if resp.StatusCode == http.StatusOK {
		require.NoError(b.t, json.Unmarshal(data, &out), string(data))
	}
	return out.Value, resp.StatusCode
}

// stream is an open connect server stream: its messages, and how it ended.
type stream struct {
	messages chan string
	end      chan string
	cancel   context.CancelFunc
}

// ends of a stream, as the Go client sees them.
const (
	endEOF           = "eof"
	endUnexpectedEOF = "unexpected-eof"
	endReset         = "reset"
	endFrame         = "end-stream-frame"
)

// openStream opens a connect server stream, with an empty request message, and reads its frames until it ends.
func (b *bed) openStream(port int, path string) *stream {
	b.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	b.t.Cleanup(cancel)
	s := &stream{messages: make(chan string, 64), end: make(chan string, 1), cancel: cancel}
	body := append([]byte{0, 0, 0, 0, 2}, '{', '}')
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+b.addr(port)+path, bytes.NewReader(body))
	require.NoError(b.t, err)
	req.Header.Set("Content-Type", "application/connect+json")
	req.Header.Set("Connect-Protocol-Version", "1")
	go func() {
		resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
		if err != nil {
			s.end <- classify(err)
			return
		}
		defer resp.Body.Close()
		for {
			var header [5]byte
			if _, err := io.ReadFull(resp.Body, header[:]); err != nil {
				s.end <- classify(err)
				return
			}
			payload := make([]byte, binary.BigEndian.Uint32(header[1:]))
			if _, err := io.ReadFull(resp.Body, payload); err != nil {
				s.end <- classify(err)
				return
			}
			if header[0]&2 != 0 {
				s.end <- endFrame + ":" + string(payload)
				return
			}
			s.messages <- string(payload)
		}
	}()
	return s
}

func classify(err error) string {
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return endUnexpectedEOF
	case errors.Is(err, io.EOF):
		return endEOF
	case errors.Is(err, syscall.ECONNRESET):
		return endReset
	default:
		return "other:" + err.Error()
	}
}

// first returns the first message of the stream.
func (s *stream) first(t *testing.T) string {
	t.Helper()
	select {
	case m := <-s.messages:
		return m
	case end := <-s.end:
		t.Fatalf("the stream ended before its first message: %s", end)
	case <-time.After(15 * time.Second):
		t.Fatal("the stream sent no message")
	}
	return ""
}

// ended waits for the stream to end, and returns how it ended and every message it sent after the first.
func (s *stream) ended(t *testing.T, within time.Duration) (end string, messages []string) {
	t.Helper()
	select {
	case end = <-s.end:
	case <-time.After(within):
		t.Fatal("the stream did not end")
	}
	for {
		select {
		case m := <-s.messages:
			messages = append(messages, m)
		default:
			return end, messages
		}
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("TESTBED_UPDATE_GOLDEN") != "" {
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got), "%s differs from the recorded bytes", name)
}

// TestTestbedContract runs the contract against the image named by TESTBED_IMAGE.
func TestTestbedContract(t *testing.T) {
	t.Run("api", testAPI)
	t.Run("outage", testOutage)
	t.Run("files", testFiles)
	t.Run("configs", testConfigs)
	t.Run("change-after-restart", testChangeAfterRestart)
	t.Run("control-port-opens-ready", testControlPortOpensReady)
}

// testAPI (C1): the four calls, with GET and POST: status code, content type and exact body.
func testAPI(t *testing.T) {
	b := startBed(t)
	const typ = "application/json"

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		require.Equal(t, reply{200, typ, ok("flagd started successfully")}, b.call(method, "/start?config=default"), method)
		require.Equal(t, reply{200, typ, ok("flagd started successfully")}, b.call(method, "/start"), method+" without a configuration")
		require.Equal(t, reply{200, typ, ok("flagd stopped successfully")}, b.call(method, "/stop"), method)
		b.requireRefused(portEvaluation, portManagement, portSync)
		require.Equal(t, reply{200, typ, ok("flagd started successfully")}, b.call(method, "/start?config=default"), method)
		require.Equal(t, reply{200, typ, ok("flagd will restart in 0 seconds")}, b.call(method, "/restart?seconds=0"), method)
		b.waitReady(20 * time.Second)
	}

	// the default delay is 5 seconds, and the answer does not wait for it
	started := time.Now()
	require.Equal(t, reply{200, typ, ok("flagd will restart in 5 seconds")}, b.call(http.MethodGet, "/restart"))
	require.Less(t, time.Since(started), 2*time.Second, "the answer comes at once")
	b.requireRefused(portEvaluation)
	b.waitReady(30 * time.Second)

	invalid := reply{400, typ, `{"status":"error","message":"'seconds' must be a non-negative integer"}` + "\n"}
	for _, seconds := range []string{"x", "-1", "1.5"} {
		require.Equal(t, invalid, b.call(http.MethodGet, "/restart?seconds="+seconds), seconds)
	}

	// a later call cancels a pending restart: the restart that is 4 s away never happens, so 8013 is not refused again
	// for longer than that
	t.Run("a later restart cancels a pending one", func(t *testing.T) {
		require.Equal(t, 200, b.call(http.MethodGet, "/restart?seconds=4").code)
		require.Equal(t, 200, b.call(http.MethodGet, "/restart?seconds=0").code)
		b.waitReady(20 * time.Second)
		b.requireNeverRefused(portEvaluation, 6*time.Second)
	})
	t.Run("stop cancels a pending restart", func(t *testing.T) {
		require.Equal(t, 200, b.call(http.MethodGet, "/restart?seconds=4").code)
		require.Equal(t, 200, b.call(http.MethodGet, "/stop").code)
		b.requireRefused(portEvaluation)
		b.requireStaysRefused(portEvaluation, 6*time.Second)
		require.Equal(t, 200, b.call(http.MethodGet, "/start?config=default").code)
		b.waitReady(20 * time.Second)
	})
	t.Run("start cancels a pending restart", func(t *testing.T) {
		require.Equal(t, 200, b.call(http.MethodGet, "/restart?seconds=4").code)
		require.Equal(t, 200, b.call(http.MethodGet, "/start?config=default").code)
		b.waitReady(20 * time.Second)
		b.requireNeverRefused(portEvaluation, 6*time.Second)
	})

	// the three changes toggle the flag, whatever the method
	for i, step := range []struct{ method, variant string }{{http.MethodGet, "bar"}, {http.MethodPost, "foo"}, {http.MethodGet, "bar"}} {
		require.Equal(t, reply{200, typ, ok("Default variant successfully changed to '" + step.variant + `'\n`)},
			b.call(step.method, "/change"), "change %d", i+1)
	}

	t.Run("an unknown configuration (quirks 1, 2 and 4)", func(t *testing.T) {
		if kind(t) == kindRelay {
			// quirk 2 fixed: the answer is at once, and names the configuration
			started := time.Now()
			failed := b.call(http.MethodGet, "/start?config=nope")
			require.Equal(t, 500, failed.code)
			require.Equal(t, typ, failed.ctype)
			var answer struct{ Status, Message string }
			require.NoError(t, json.Unmarshal([]byte(failed.body), &answer))
			require.Equal(t, "error", answer.Status)
			require.Contains(t, answer.Message, `Failed to start flagd: unknown configuration "nope"`)
			require.Less(t, time.Since(started), 100*time.Millisecond, "an unknown configuration answers at once")

			// quirk 4 fixed: the running flagd is not touched
			_, code := b.resolveString(nil, "changing-flag")
			require.Equal(t, 200, code, "8013 still serves after the failed start")

			// quirk 1 fixed: the failed name is not remembered
			require.Equal(t, 200, b.call(http.MethodGet, "/restart?seconds=0").code)
			b.waitReady(20 * time.Second)
			require.Equal(t, reply{200, typ, ok("flagd started successfully")}, b.call(http.MethodGet, "/start"))
			return
		}

		const timedOut = `{"status":"error","message":"Failed to start flagd: flagd health check timed out"}` + "\n"
		started := time.Now()
		require.Equal(t, reply{500, typ, timedOut}, b.call(http.MethodGet, "/start?config=nope"))
		require.GreaterOrEqual(t, time.Since(started), 10*time.Second, "an unknown configuration answers after the readiness timeout")
		b.requireRefused(portEvaluation, portManagement, portSync) // quirk 4: the failed start leaves nothing listening

		// the unknown name is remembered, so a restart and a bare start fail too
		require.Equal(t, 200, b.call(http.MethodGet, "/restart?seconds=0").code)
		time.Sleep(3 * time.Second)
		require.False(t, b.ready(), "the restart started the remembered configuration")
		require.Equal(t, reply{500, typ, timedOut}, b.call(http.MethodGet, "/start"))

		require.Equal(t, reply{200, typ, ok("flagd started successfully")}, b.call(http.MethodGet, "/start?config=default"))
	})
}

// testOutage (C2): during /stop and /restart the flagd ports refuse connections while the control port answers; open
// streams end with a plain close, and everything works again afterwards.
func testOutage(t *testing.T) {
	b := startBed(t)

	events := b.openStream(portEvaluation, evaluationPath+"EventStream")
	t.Logf("first event: %s", events.first(t))
	syncs := b.openStream(portSync, syncPath)
	require.Contains(t, syncs.first(t), "flagConfiguration")

	require.Equal(t, 200, b.call(http.MethodGet, "/stop").code)
	b.requireRefused(portEvaluation, portManagement, portSync)
	require.Equal(t, 400, b.call(http.MethodGet, "/restart?seconds=x").code, "the control port answers during the outage")

	// the launchpad's observed end of each stream is recorded, and the relay testbed must end them the same way
	var ends bytes.Buffer
	for _, named := range []struct {
		name string
		s    *stream
	}{{"EventStream", events}, {"SyncFlags", syncs}} {
		end, after := named.s.ended(t, 5*time.Second)
		t.Logf("%s ended with %s after %d more messages", named.name, end, len(after))
		fmt.Fprintf(&ends, "%s: %s\n", named.name, end)
		for _, m := range after {
			require.NotContains(t, m, "provider_shutdown", "%s: no goodbye before the close", named.name)
		}
	}
	golden(t, "outage-stream-ends.golden", ends.Bytes())

	require.Equal(t, 200, b.call(http.MethodGet, "/start?config=default").code)
	b.waitReady(20 * time.Second)

	require.Equal(t, 200, b.call(http.MethodGet, "/restart?seconds=3").code)
	b.requireRefused(portEvaluation, portManagement, portSync)
	require.Equal(t, 400, b.call(http.MethodGet, "/restart?seconds=x").code)
	b.waitReady(20 * time.Second)

	events = b.openStream(portEvaluation, evaluationPath+"EventStream")
	events.first(t)
	syncs = b.openStream(portSync, syncPath)
	require.Contains(t, syncs.first(t), "flagConfiguration")
}

// testFiles (C3): /flags/allFlags.json exists at start, is gone during a restart and is back afterwards, and its bytes
// at start and after one and two /change calls are the recorded goldens.
func testFiles(t *testing.T) {
	b := startBed(t)

	start, err := b.allFlags()
	require.NoError(t, err, "the file exists at start")
	golden(t, "allFlags.start.json", start)

	require.Equal(t, 200, b.call(http.MethodGet, "/restart?seconds=3").code)
	_, err = b.allFlags()
	require.ErrorIs(t, err, os.ErrNotExist, "the file is gone during the restart")
	b.waitReady(20 * time.Second)
	again, err := b.allFlags()
	require.NoError(t, err, "the file is back afterwards")
	require.Equal(t, string(start), string(again))

	changing := "/rawflags/changing-flag.json"
	untouched := b.rawFile(changing)

	require.Equal(t, 200, b.call(http.MethodGet, "/change").code)
	one, err := b.allFlags()
	require.NoError(t, err)
	golden(t, "allFlags.after-one-change.json", one)
	require.NotEqual(t, string(start), string(one))

	t.Run("source file layout (quirk 3)", func(t *testing.T) {
		if kind(t) == kindRelay {
			require.Equal(t, untouched, b.rawFile(changing), "/change leaves the source file as it was")
			return
		}
		require.NotEqual(t, untouched, b.rawFile(changing))
		golden(t, "changing-flag.after-one-change.launchpad.json", []byte(b.rawFile(changing)))
	})

	require.Equal(t, 200, b.call(http.MethodGet, "/change").code)
	two, err := b.allFlags()
	require.NoError(t, err)
	golden(t, "allFlags.after-two-changes.json", two)
}

// testConfigs (C4): the configurations the suites switch to.
func testConfigs(t *testing.T) {
	b := startBed(t)
	const started = `{"status":"success","message":"flagd started successfully"}` + "\n"

	t.Run("ssl", func(t *testing.T) {
		require.Equal(t, started, b.call(http.MethodGet, "/start?config=ssl").body)
		ca := filepath.Join(t.TempDir(), "ca.crt")
		docker(t, "cp", b.id+":/ssl/custom-root-cert.crt", ca)
		pem, err := os.ReadFile(ca)
		require.NoError(t, err)
		value, code := b.resolveString(pem, "changing-flag")
		require.Equal(t, 200, code)
		require.Equal(t, "foo", value, "TLS with the custom root CA")
		if resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + b.addr(portEvaluation) + "/"); err == nil {
			defer resp.Body.Close()
			require.GreaterOrEqual(t, resp.StatusCode, 400, "plain HTTP is not served on the TLS port")
		}
	})

	firstSync := func(t *testing.T) string {
		s := b.openStream(portSync, syncPath)
		message := s.first(t)
		s.cancel()
		return message
	}

	syncMetadata := func() (code int, body string) {
		req, err := http.NewRequest(http.MethodPost, "http://"+b.addr(portSync)+"/flagd.sync.v1.FlagSyncService/GetMetadata", strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Connect-Protocol-Version", "1")
		resp, err := (&http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(data)
	}

	t.Run("sync-payload", func(t *testing.T) {
		require.Equal(t, started, b.call(http.MethodGet, "/start?config=default").body)
		code, body := syncMetadata()
		require.Equal(t, 200, code)
		require.Contains(t, body, "injectedmetadata", "the default configuration serves the sync metadata")
		require.Equal(t, started, b.call(http.MethodGet, "/start?config=sync-payload").body)
		code, body = syncMetadata()
		require.Equal(t, 501, code, "sync-payload disables the metadata endpoint")
		require.Contains(t, body, "metadata endpoint disabled")
		require.Contains(t, firstSync(t), "flagConfiguration", "the flags still sync")
	})

	t.Run("metadata", func(t *testing.T) {
		require.Equal(t, started, b.call(http.MethodGet, "/start?config=metadata").body)
		message := firstSync(t)
		require.Contains(t, message, "combined-metadata-flag")
		require.NotContains(t, message, "changing-flag", "only the metadata flag set is served")
		_, code := b.resolveString(nil, "changing-flag")
		require.NotEqual(t, 200, code)
		require.Equal(t, started, b.call(http.MethodGet, "/start?config=default").body)
	})
}

// testChangeAfterRestart (C5): the sequence that broke a gate-only restart: /change, an 8 s restart, then /change
// again. An evaluation of changing-flag follows both toggles.
func testChangeAfterRestart(t *testing.T) {
	b := startBed(t)

	value, code := b.resolveString(nil, "changing-flag")
	require.Equal(t, 200, code)
	require.Equal(t, "foo", value)

	eventually := func(want string) {
		t.Helper()
		require.Eventually(t, func() bool {
			value, code := b.resolveString(nil, "changing-flag")
			return code == 200 && value == want
		}, 10*time.Second, 200*time.Millisecond, "changing-flag evaluates to %s", want)
	}

	require.Equal(t, 200, b.call(http.MethodGet, "/change").code)
	eventually("bar")
	require.Equal(t, 200, b.call(http.MethodGet, "/restart?seconds=8").code)
	b.requireRefused(portEvaluation)
	b.waitReady(30 * time.Second)
	eventually("bar")
	require.Equal(t, 200, b.call(http.MethodGet, "/change").code)
	eventually("foo")
}

// testControlPortOpensReady: the control port never opens before the default configuration has started. From the start of
// the container the port is polled, and the moment it first accepts, the management port must already be ready. (The
// other cases wait for readiness first, and so cannot see a control port that opens early.)
func testControlPortOpensReady(t *testing.T) {
	image := os.Getenv("TESTBED_IMAGE")
	if image == "" {
		t.Skip("the testbed contract needs a testbed image; make testbed-contract IMAGE=<image> sets TESTBED_IMAGE")
	}
	id := docker(t, "run", "-d", "-v", t.TempDir()+":/flags", image)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	ip := docker(t, "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", id)
	require.NotEmpty(t, ip, "the container has no address")
	b := &bed{t: t, id: id, ip: ip}

	deadline := time.Now().Add(60 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", b.addr(portControl), time.Second)
		if err == nil {
			conn.Close()
			break
		}
		require.True(t, time.Now().Before(deadline), "the control port never opens")
		time.Sleep(2 * time.Millisecond)
	}
	require.True(t, b.ready(), "the control port accepted before flagd was ready")
}
