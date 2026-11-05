package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var updateStartupLogs = flag.Bool("update-startup-logs", false, "rewrite testdata/startup_logs.golden from the current binary")

const startupLogsGolden = "testdata/startup_logs.golden"

// lockedBuffer is written by the command's output copier while the test reads its length.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// freePort returns a port that was free a moment ago.
// issuedPorts are the ports freePort has handed out in this process. A port is free again the moment it is closed, so
// the next call can get the same one, and one start would listen twice on it.
var issuedPorts sync.Map

func freePort(t *testing.T) string {
	t.Helper()
	// a port the kernel hands out again stays open until a new one comes, so the next listen cannot get it
	var held []net.Listener
	defer func() {
		for _, h := range held {
			_ = h.Close()
		}
	}()
	for range 1000 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := l.Addr().(*net.TCPAddr).Port
		if _, taken := issuedPorts.LoadOrStore(port, true); taken {
			held = append(held, l)
			continue
		}
		_ = l.Close()
		return fmt.Sprint(port)
	}
	t.Fatal("no free port that was not handed out before")
	return ""
}

// renderLogLine reduces one log line to what an operator greps for: level, message and fields. The timestamp and the
// caller (a source file and line, which refactoring moves) are left out. Lines are in zap's JSON or console encoding.
func renderLogLine(line, format string) string {
	if format == "json" {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return "not a log line: " + line
		}
		level, msg := entry["level"], entry["msg"]
		delete(entry, "level")
		delete(entry, "msg")
		keys := make([]string, 0, len(entry))
		for k := range entry {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var fields []string
		for _, k := range keys {
			switch k {
			case "ts", "caller":
				fields = append(fields, k+"=<masked>")
			default:
				fields = append(fields, fmt.Sprintf("%s=%v", k, entry[k]))
			}
		}
		return fmt.Sprintf("%v | %v | %s", level, msg, strings.Join(fields, " "))
	}
	// console: time, level, caller, message and the fields as JSON, separated by tabs
	cols := strings.Split(line, "\t")
	if len(cols) < 4 {
		return "not a log line: " + line
	}
	fields := ""
	if len(cols) > 4 {
		fields = cols[4]
	}
	return fmt.Sprintf("<time>\t%s\t<caller>\t%s\t%s", cols[1], cols[3], fields)
}

// renderLogPhase renders the log lines of one phase sorted, because the service endpoints start and stop concurrently.
func renderLogPhase(lines, format string, replace *strings.Replacer) string {
	var out []string
	for _, line := range strings.Split(lines, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, replace.Replace(renderLogLine(line, format)))
	}
	sort.Strings(out)
	return strings.Join(out, "\n") + "\n"
}

// TestStartupLogCharacterization pins what a normal start and a SIGTERM shutdown print, in both log formats: the
// level, message and fields of every log line, the console encoding's columns, the banner on stdout, and the exit
// code. Operators grep and alert on these lines, so a flagd-full built by the Builder keeps them (drop-in rule).
func TestStartupLogCharacterization(t *testing.T) {
	tmp := t.TempDir()
	bin := buildFlagd(t, tmp)
	flags := filepath.Join(tmp, "flags.json")
	require.NoError(t, os.WriteFile(flags,
		[]byte(`{"flags":{"f":{"state":"ENABLED","variants":{"on":true},"defaultVariant":"on"}}}`), 0o600))

	var out strings.Builder
	for _, format := range []string{"json", "console"} {
		ports := map[string]string{}
		for _, name := range []string{"port", "management-port", "sync-port", "ofrep-port"} {
			ports[name] = freePort(t)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		cmd := exec.CommandContext(ctx, bin, "start", "--log-format", format, "--uri", "file:"+flags,
			"--port", ports["port"], "--management-port", ports["management-port"],
			"--sync-port", ports["sync-port"], "--ofrep-port", ports["ofrep-port"])
		cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
		var stdout, stderr lockedBuffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		require.NoError(t, cmd.Start())

		ready := func() bool {
			res, err := http.Get("http://127.0.0.1:" + ports["management-port"] + "/readyz") //nolint:noctx
			if err != nil {
				return false
			}
			_ = res.Body.Close()
			return res.StatusCode == http.StatusOK
		}
		require.Eventually(t, ready, 30*time.Second, 50*time.Millisecond, "flagd did not become ready: %s", stderr.String())
		// the endpoints log while they start, concurrently with readiness
		time.Sleep(500 * time.Millisecond)
		startup := stderr.String()

		require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
		err := cmd.Wait()
		cancel()
		exit := 0
		if err != nil {
			exitErr := &exec.ExitError{}
			require.ErrorAs(t, err, &exitErr, "flagd did not exit: %v", err)
			exit = exitErr.ExitCode()
		}
		shutdown := strings.TrimPrefix(stderr.String(), startup)

		replace := strings.NewReplacer(
			tmp, "<tmp>",
			ports["port"], "<port>", ports["management-port"], "<management-port>",
			ports["sync-port"], "<sync-port>", ports["ofrep-port"], "<ofrep-port>",
		)
		fmt.Fprintf(&out, "== --log-format %s\nexit after SIGTERM: %d\n", format, exit)
		fmt.Fprintf(&out, "-- stdout\n%s", stdout.String())
		fmt.Fprintf(&out, "-- startup log, sorted\n%s", renderLogPhase(startup, format, replace))
		fmt.Fprintf(&out, "-- shutdown log, sorted\n%s", renderLogPhase(shutdown, format, replace))
	}

	if *updateStartupLogs {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(startupLogsGolden, []byte(out.String()), 0o600))
		return
	}
	want, err := os.ReadFile(startupLogsGolden)
	require.NoError(t, err, "record the current logs with `go test -run TestStartupLogCharacterization -update-startup-logs`")
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	require.Equal(t, stampedLogs(string(want)), stampedLogs(out.String()))
}

// logStamp matches the version, commit and date a log line of flagd version carries, in the console and the JSON
// encoding.
var logStamp = regexp.MustCompile(`(version|commit|date)=\S+|"(version|commit|date)": "[^"]*"`)

// stampedLogs leaves the stamps of the flagd version line out of the comparison when FLAGD_BINARY names a binary the
// Builder made: it carries the stamps of its manifest, not those of the pin's own build.
func stampedLogs(s string) string {
	if os.Getenv("FLAGD_BINARY") == "" {
		return s
	}
	return logStamp.ReplaceAllStringFunc(s, func(m string) string {
		if i := strings.IndexAny(m, "=:"); i >= 0 {
			return m[:i+1] + " <stamp>"
		}
		return m
	})
}
