package distributions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The tests below run the flagd-full that the Builder makes, with the webhook event sink and the event collector it
// lists (UC-021): evaluation and configuration change events reach a webhook, the credential comes from the environment,
// in-process events arrive through the collector, and a flagd-full without the keys of these components opens the ports
// it always has.

const (
	webhookToken = "Bearer s3cret-export-token"
	flagSetA     = "set-a"
	flagSetB     = "set-b"
)

// exportFlagFile writes a flag configuration with one flag in the flag set and returns its path.
func exportFlagFile(t *testing.T, dir, name, flagSet, flag, defaultVariant string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	writeExportFlags(t, path, flagSet, flag, defaultVariant)
	return path
}

func writeExportFlags(t *testing.T, path, flagSet, flag, defaultVariant string) {
	t.Helper()
	body := fmt.Sprintf(`{"metadata":{"flagSetId":%q},"flags":{%q:{"state":"ENABLED","variants":{"on":true,"off":false},"defaultVariant":%q}}}`,
		flagSet, flag, defaultVariant)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

// webhookReceiver is a webhook that keeps the events it is sent. It refuses a request without the token, and can stall or
// refuse everything else.
type webhookReceiver struct {
	srv *httptest.Server

	mu           sync.Mutex
	events       []map[string]any
	unauthorized int
	gate         chan struct{} // while set, requests wait for it
	status       int           // 0 accepts
}

func newWebhookReceiver(t *testing.T) *webhookReceiver {
	t.Helper()
	r := &webhookReceiver{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		gate, status := r.gate, r.status
		r.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-req.Context().Done():
				return
			}
		}
		if req.Header.Get("Authorization") != webhookToken {
			r.mu.Lock()
			r.unauthorized++
			r.mu.Unlock()
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		var batch []map[string]any
		if err := json.NewDecoder(req.Body).Decode(&batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.events = append(r.events, batch...)
		r.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// received returns the events of one kind the webhook has so far.
func (r *webhookReceiver) received(kind string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, e := range r.events {
		if e["kind"] == kind {
			out = append(out, e)
		}
	}
	return out
}

func (r *webhookReceiver) stall() (release func()) {
	gate := make(chan struct{})
	r.mu.Lock()
	r.gate = gate
	r.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }
}

// exporting is a running flagd-full.
type exporting struct {
	ofrep, management string // base URLs
	pid               int
	ports             []int
	out               *lockedBuffer
}

type exportOptions struct {
	sources []string
	// config is the configuration file; "" starts without one
	config string
	env    []string
}

func exportEnv() []string {
	return []string{"HOME=" + os.TempDir(), "PATH=" + os.Getenv("PATH"), "WEBHOOK_TOKEN=" + webhookToken}
}

// flagdFullArgs builds the arguments of flagd start with free ports, and returns them with the ports.
func flagdFullArgs(t *testing.T, o exportOptions) (args []string, ports map[string]int) {
	t.Helper()
	ports = map[string]int{}
	args = []string{"start"}
	for _, name := range []string{"port", "management-port", "sync-port", "ofrep-port"} {
		ports[name] = freePort(t)
		args = append(args, "--"+name, fmt.Sprint(ports[name]))
	}
	for _, source := range o.sources {
		args = append(args, "--uri", "file:"+source)
	}
	if o.config != "" {
		file := filepath.Join(t.TempDir(), "flagd.yaml")
		require.NoError(t, os.WriteFile(file, []byte(o.config), 0o600))
		args = append(args, "--config", file)
	}
	return args, ports
}

func startExporting(t *testing.T, o exportOptions) *exporting {
	t.Helper()
	path := filepath.Join(root(t), "distributions", "flagd-full", "manifest.yaml")
	b := build(t, path)
	require.NoError(t, b.err, b.out)

	args, ports := flagdFullArgs(t, o)
	e := &exporting{out: &lockedBuffer{}}
	cmd := exec.Command(binary(t, path), args...)
	cmd.Env = append(exportEnv(), o.env...)
	cmd.Stdout, cmd.Stderr = e.out, e.out
	require.NoError(t, cmd.Start())
	e.pid = cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		require.NotContains(t, e.out.String(), webhookToken, "the credential is in no output of flagd")
	})

	e.ofrep = fmt.Sprintf("http://127.0.0.1:%d", ports["ofrep-port"])
	e.management = fmt.Sprintf("http://127.0.0.1:%d", ports["management-port"])
	e.ports = []int{ports["port"], ports["management-port"], ports["sync-port"], ports["ofrep-port"]}
	require.Eventually(t, func() bool { return e.status("/readyz") == http.StatusOK }, 60*time.Second, 200*time.Millisecond,
		"flagd-full becomes ready: %s", e.out)
	return e
}

func (e *exporting) status(path string) int {
	resp, err := http.Get(e.management + path) //nolint:noctx // a local probe
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (e *exporting) metrics(t *testing.T) string {
	t.Helper()
	resp, err := http.Get(e.management + "/metrics") //nolint:noctx // a local probe
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

// evaluate resolves a flag through OFREP and returns the variant.
func (e *exporting) evaluate(t *testing.T, flag string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.ofrep+"/ofrep/v1/evaluate/flags/"+flag, strings.NewReader(`{"context":{}}`)) //nolint:noctx // a local request
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var result ofrepResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Equal(t, http.StatusOK, resp.StatusCode, "%s: %+v", flag, result)
	return result.Variant
}

func webhookSinkConfig(url string, extra string) string {
	return fmt.Sprintf(`event-sinks:
  - type: webhook
    url: %s
    flushInterval: 100ms
    headers:
      Authorization: {env: WEBHOOK_TOKEN}
%s`, url, extra)
}

// exportListeningPorts returns the TCP ports the process listens on, from /proc.
func exportListeningPorts(t *testing.T, pid int) []int {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the listening sockets of a process are read from /proc")
	}
	inodes := map[string]bool{}
	fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	require.NoError(t, err)
	for _, fd := range fds {
		link, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, fd.Name()))
		if err == nil && strings.HasPrefix(link, "socket:[") {
			inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] = true
		}
	}
	seen := map[int]bool{}
	for _, table := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/net/%s", pid, table))
		require.NoError(t, err)
		for _, line := range strings.Split(string(data), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" || !inodes[fields[9]] { // 0A is LISTEN
				continue
			}
			var port int
			_, err := fmt.Sscanf(fields[1][strings.LastIndex(fields[1], ":")+1:], "%X", &port)
			require.NoError(t, err)
			seen[port] = true
		}
	}
	var ports []int
	for p := range seen {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

// TestFlagdFullOpensTodaysPorts pins that the flagd-full that lists the event collector opens the four ports it always
// has, and no fifth one, until the operator gives the collector a port. With one it listens there too.
func TestFlagdFullOpensTodaysPorts(t *testing.T) {
	dir := t.TempDir()
	source := exportFlagFile(t, dir, "a.json", flagSetA, "a", "on")

	plain := startExporting(t, exportOptions{sources: []string{source}})
	want := append([]int(nil), plain.ports...)
	sort.Ints(want)
	require.Equal(t, want, exportListeningPorts(t, plain.pid), "no port besides the four of flagd's flags")

	collectorPort := freePort(t)
	withCollector := startExporting(t, exportOptions{sources: []string{source}, config: fmt.Sprintf("event-collector:\n  port: %d\n", collectorPort)})
	want = append(append([]int(nil), withCollector.ports...), collectorPort)
	sort.Ints(want)
	require.Equal(t, want, exportListeningPorts(t, withCollector.pid), "the event-collector key opens its port")

	configured := startExporting(t, exportOptions{sources: []string{source}, config: "event-collector: {}\nevent-sinks: []\nendpoint-settings: {}\nmiddleware: []\n"})
	want = append([]int(nil), configured.ports...)
	sort.Ints(want)
	require.Equal(t, want, exportListeningPorts(t, configured.pid), "the keys left empty open nothing")
}

// TestExportToWebhook covers UC-021's main scenario on the built flagd-full: a webhook sink with a credential from the
// environment, evaluation events from the endpoints and configuration change events from the runtime, events of an
// in-process provider through the event collector, and the events of every flag set (BR-007).
func TestExportToWebhook(t *testing.T) {
	dir := t.TempDir()
	sourceA := exportFlagFile(t, dir, "a.json", flagSetA, "a", "on")
	sourceB := exportFlagFile(t, dir, "b.json", flagSetB, "b", "off")
	hook := newWebhookReceiver(t)
	collectorPort := freePort(t)
	e := startExporting(t, exportOptions{
		sources: []string{sourceA, sourceB},
		config:  webhookSinkConfig(hook.srv.URL, "") + fmt.Sprintf("event-collector:\n  port: %d\n", collectorPort),
	})
	collector := fmt.Sprintf("http://127.0.0.1:%d/events/v1", collectorPort)

	t.Run("evaluations of every flag set reach the webhook with the credential", func(t *testing.T) {
		require.Equal(t, "on", e.evaluate(t, "a"))
		require.Equal(t, "off", e.evaluate(t, "b"))
		require.Eventually(t, func() bool { return len(hook.received("evaluation")) >= 2 }, 15*time.Second, 50*time.Millisecond,
			"the webhook receives an evaluation event per resolved flag; has %v", hook.received("evaluation"))

		bySet := map[string]map[string]any{}
		for _, ev := range hook.received("evaluation") {
			bySet[fmt.Sprint(ev["flagSet"])] = ev
		}
		require.Equal(t, "a", bySet[flagSetA]["flagKey"])
		require.Equal(t, "on", bySet[flagSetA]["variant"])
		require.Equal(t, "b", bySet[flagSetB]["flagKey"])
		require.Equal(t, "service", bySet[flagSetB]["origin"])
		require.Zero(t, hook.unauthorized, "every request carried the token from the environment")
	})

	t.Run("a configuration change reaches the webhook", func(t *testing.T) {
		before := len(hook.received("configuration_change"))
		writeExportFlags(t, sourceA, flagSetA, "a", "off")
		require.Eventually(t, func() bool { return len(hook.received("configuration_change")) > before }, 15*time.Second, 50*time.Millisecond,
			"the webhook receives a configuration change event")

		change := hook.received("configuration_change")[before]
		require.Equal(t, flagSetA, change["flagSet"])
		require.Contains(t, fmt.Sprint(change["source"]), "a.json")
		require.Equal(t, []any{"a"}, change["updated"])
	})

	t.Run("the events of an in-process provider arrive through the collector", func(t *testing.T) {
		resp, err := http.Post(collector, "application/json", strings.NewReader( //nolint:noctx // a local request
			`{"events":[{"kind":"evaluation","flagKey":"checkout","flagSet":"web","variant":"on","value":3},`+
				`{"kind":"tracking","name":"purchase","value":9.5,"details":{"currency":"EUR"}}]}`))
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusAccepted, resp.StatusCode)

		require.Eventually(t, func() bool {
			return len(hook.received("tracking")) == 1 && hasEventForFlag(hook.received("evaluation"), "checkout")
		}, 15*time.Second, 50*time.Millisecond, "the webhook receives the relayed events")
		for _, ev := range append(hook.received("tracking"), eventsForFlag(hook.received("evaluation"), "checkout")...) {
			require.Equal(t, "in-process", ev["origin"])
		}
		require.Equal(t, "purchase", hook.received("tracking")[0]["name"])
	})

	t.Run("an invalid event is rejected and a valid one of the same batch is delivered", func(t *testing.T) {
		resp, err := http.Post(collector, "application/json", strings.NewReader( //nolint:noctx // a local request
			`{"events":[{"kind":"evaluation","flagKey":"partial"},{"kind":"evaluation"}]}`))
		require.NoError(t, err)
		var out struct {
			Accepted int `json:"accepted"`
			Rejected []struct {
				Index int `json:"index"`
			} `json:"rejected"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		require.Equal(t, 1, out.Accepted)
		require.Len(t, out.Rejected, 1)
		require.Equal(t, 1, out.Rejected[0].Index)
		require.Eventually(t, func() bool { return hasEventForFlag(hook.received("evaluation"), "partial") }, 15*time.Second, 50*time.Millisecond)
	})
}

func hasEventForFlag(events []map[string]any, flag string) bool {
	return len(eventsForFlag(events, flag)) > 0
}

func eventsForFlag(events []map[string]any, flag string) []map[string]any {
	var out []map[string]any
	for _, ev := range events {
		if ev["flagKey"] == flag {
			out = append(out, ev)
		}
	}
	return out
}

// TestKindsNarrowWhatTheWebhookReceives pins that the kinds setting narrows the events a sink gets on the built binary.
func TestKindsNarrowWhatTheWebhookReceives(t *testing.T) {
	source := exportFlagFile(t, t.TempDir(), "a.json", flagSetA, "a", "on")
	hook := newWebhookReceiver(t)
	e := startExporting(t, exportOptions{sources: []string{source}, config: webhookSinkConfig(hook.srv.URL, "    kinds: [configuration_change]\n")})

	e.evaluate(t, "a")
	writeExportFlags(t, source, flagSetA, "a", "off")
	require.Eventually(t, func() bool { return len(hook.received("configuration_change")) > 0 }, 15*time.Second, 50*time.Millisecond)
	time.Sleep(500 * time.Millisecond)
	require.Empty(t, hook.received("evaluation"), "a sink narrowed to configuration changes gets no evaluation event")
}

// exportStoppedStart runs flagd-full until it stops and returns its output and exit code. A start that does not stop fails.
func exportStoppedStart(t *testing.T, o exportOptions) (output string, exitCode int) {
	t.Helper()
	path := filepath.Join(root(t), "distributions", "flagd-full", "manifest.yaml")
	b := build(t, path)
	require.NoError(t, b.err, b.out)

	args, _ := flagdFullArgs(t, o)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary(t, path), args...)
	cmd.Env = append(exportEnv(), o.env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	require.NotEqual(t, context.DeadlineExceeded, ctx.Err(), "flagd-full was expected to stop at startup: %s", out.String())
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, out.String())
	return out.String(), exit.ExitCode()
}

// TestInvalidEventSinkSettingStops pins UC-021 A2 on the built binary: a faulty event sink setting stops flagd before it
// serves, names the sink and the setting, and never prints the credential.
func TestInvalidEventSinkSettingStops(t *testing.T) {
	source := exportFlagFile(t, t.TempDir(), "a.json", flagSetA, "a", "on")
	secretFile := filepath.Join(t.TempDir(), "unreadable")
	tests := map[string]struct {
		config string
		env    []string
		want   []string
	}{
		"unknown sink type": {
			"event-sinks:\n  - type: kafka\n    url: http://x\n", nil,
			[]string{"event-sinks[0]", `"kafka"`, "webhook"},
		},
		"undeclared kind": {
			"event-sinks:\n  - type: webhook\n    url: http://localhost:1\n    kinds: [metric]\n", nil,
			[]string{`event sink "webhook"`, `"kinds"`, `"metric"`, "configuration_change"},
		},
		"environment variable not set": {
			"event-sinks:\n  - type: webhook\n    url: http://localhost:1\n    headers: {Authorization: {env: RELAY_EXPORT_UNSET}}\n", nil,
			[]string{`event sink "webhook"`, `setting "headers.Authorization"`, "RELAY_EXPORT_UNSET"},
		},
		"unreadable credential file": {
			"event-sinks:\n  - type: webhook\n    url: http://localhost:1\n    headers: {Authorization: {file: " + secretFile + "}}\n", nil,
			[]string{`event sink "webhook"`, `setting "headers.Authorization"`},
		},
		"invalid url": {
			"event-sinks:\n  - type: webhook\n    url: ftp://nowhere\n", nil,
			[]string{`event sink "webhook"`, "ftp://nowhere"},
		},
		"unknown setting": {
			"event-sinks:\n  - type: webhook\n    url: http://localhost:1\n    timout: 1s\n", nil,
			[]string{`event sink "webhook"`, `"timout"`},
		},
		"unknown event collector setting": {
			"event-collector:\n  prot: 1\n", nil,
			[]string{"event-collector", `"prot"`},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			output, code := exportStoppedStart(t, exportOptions{sources: []string{source}, config: tt.config, env: tt.env})
			require.NotZero(t, code)
			for _, want := range tt.want {
				require.Contains(t, output, want)
			}
			require.NotContains(t, output, webhookToken)
		})
	}
}

// TestReceiverDownKeepsReadiness pins UC-021 A6 on the built binary: when the receiver refuses every batch, flagd stays
// ready and keeps evaluating, and the events it could not deliver are counted on the management port.
func TestReceiverDownKeepsReadiness(t *testing.T) {
	source := exportFlagFile(t, t.TempDir(), "a.json", flagSetA, "a", "on")
	hook := newWebhookReceiver(t)
	hook.mu.Lock()
	hook.status = http.StatusServiceUnavailable
	hook.mu.Unlock()
	e := startExporting(t, exportOptions{sources: []string{source}, config: webhookSinkConfig(hook.srv.URL, "    maxAttempts: 2\n")})

	for i := 0; i < 5; i++ {
		require.Equal(t, "on", e.evaluate(t, "a"), "evaluation goes on")
	}
	require.Eventually(t, func() bool {
		return strings.Contains(e.metrics(t), "retries_exhausted")
	}, 30*time.Second, 200*time.Millisecond, "the events the receiver refused are counted as dropped")
	require.Equal(t, http.StatusOK, e.status("/readyz"), "a receiver that is down does not change readiness")
	require.Equal(t, http.StatusOK, e.status("/healthz"))
}

// TestFullBufferDropsAndReportsOnMetrics pins UC-021 A5 on the built binary: with a buffer of one event and a receiver
// that does not answer, evaluation is not slowed and the dropped events are counted on /metrics.
func TestFullBufferDropsAndReportsOnMetrics(t *testing.T) {
	source := exportFlagFile(t, t.TempDir(), "a.json", flagSetA, "a", "on")
	hook := newWebhookReceiver(t)
	release := hook.stall()
	defer release()
	e := startExporting(t, exportOptions{sources: []string{source}, config: webhookSinkConfig(hook.srv.URL, "    bufferSize: 1\n    batchSize: 1\n")})

	began := time.Now()
	for i := 0; i < 200; i++ {
		e.evaluate(t, "a")
	}
	require.Less(t, time.Since(began), 20*time.Second, "evaluation does not wait for the receiver")
	require.Eventually(t, func() bool {
		metrics := e.metrics(t)
		return strings.Contains(metrics, "event_sink_dropped") && strings.Contains(metrics, `reason="buffer_full"`)
	}, 15*time.Second, 200*time.Millisecond, "dropped events are counted on the management port")
}
