package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var updateCLI = flag.Bool("update-cli", false, "rewrite testdata/cli.golden from the current binary")

const cliGolden = "testdata/cli.golden"

// buildFlagd builds the binary of flagd's main package into dir, with the version stamps of a release build.
// A binary the Builder made is used when FLAGD_BINARY names it; it carries the stamps of its manifest, so the stamped
// line of flagd version is not compared then.
var stamp = regexp.MustCompile(`flagd: \S+ \([^)]*\), built at: \S+`)

func stamped(s string) string {
	if os.Getenv("FLAGD_BINARY") == "" {
		return s
	}
	return stamp.ReplaceAllString(s, "flagd: <stamp>")
}

func buildFlagd(t *testing.T, dir string) string {
	t.Helper()
	if bin := os.Getenv("FLAGD_BINARY"); bin != "" {
		return bin
	}
	bin := filepath.Join(dir, "flagd")
	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", bin,
		"-ldflags", "-X main.version=v9.9.9 -X main.commit=abc1234 -X main.date=2026-01-02T03:04:05Z",
		"github.com/open-feature/flagd/flagd")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "building flagd: %s", out)
	return bin
}

type run struct {
	exit   int
	stdout string
	stderr string
}

// execute runs the binary with an empty home directory, so that no configuration file of the machine is read.
func execute(t *testing.T, bin string, args ...string) run {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := run{stdout: stdout.String(), stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		r.exit = exit.ExitCode()
	case err != nil:
		t.Fatalf("running flagd %v: %v", args, err)
	}
	return r
}

var (
	stdLogLine = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} (.*)$`)
	portNumber = regexp.MustCompile(`127\.0\.0\.1:\d+|:\d{4,5}\b`)
)

// firstError is the first error line a run printed: a cobra error, a line of Go's standard logger, or a log line of
// level error or fatal.
func firstError(r run) string {
	for _, line := range strings.Split(r.stdout+"\n"+r.stderr, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Error: "):
			return line
		case stdLogLine.MatchString(line):
			return stdLogLine.FindStringSubmatch(line)[1]
		case strings.HasPrefix(line, "{"):
			var entry struct{ Level, Msg string }
			if json.Unmarshal([]byte(line), &entry) == nil && (entry.Level == "error" || entry.Level == "fatal") {
				return entry.Level + ": " + entry.Msg
			}
		}
	}
	return "<no error line>"
}

func normalizeOutput(s, tmp string) string {
	s = strings.ReplaceAll(s, tmp, "<tmp>")
	return portNumber.ReplaceAllString(s, "<addr>")
}

// TestCLICharacterization pins the command line of the flagd binary: the help it prints, and for each startup failure
// the exit code and the first error line. A flagd-full built by the Builder keeps all of it (UC-001 drop-in rule).
func TestCLICharacterization(t *testing.T) {
	tmp := t.TempDir()
	bin := buildFlagd(t, tmp)
	var out strings.Builder

	for _, args := range [][]string{{"--help"}, {"start", "--help"}, {"version"}} {
		r := execute(t, bin, args...)
		fmt.Fprintf(&out, "== flagd %s\nexit %d\n%s", strings.Join(args, " "), r.exit, r.stdout)
		if r.stderr != "" {
			fmt.Fprintf(&out, "-- stderr\n%s", r.stderr)
		}
	}

	flags := filepath.Join(tmp, "flags.json")
	require.NoError(t, os.WriteFile(flags, []byte(`{"flags":{}}`), 0o600))
	invalidYAML := filepath.Join(tmp, "invalid.yaml")
	require.NoError(t, os.WriteFile(invalidYAML, []byte("port: [unclosed\n  - : :\n"), 0o600))

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer busy.Close()
	busyPort := fmt.Sprint(busy.Addr().(*net.TCPAddr).Port)
	free := func() string {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer l.Close()
		return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
	}

	failures := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"start", "--nope"}},
		{"unknown command", []string{"nope"}},
		{"invalid --sources JSON", []string{"start", "--log-format", "json", "--sources", "not-json"}},
		{"unknown URI scheme", []string{"start", "--log-format", "json", "--uri", "nope://x"}},
		{"port in use", []string{
			"start", "--log-format", "json", "--uri", "file:" + flags, "--port", busyPort,
			"--management-port", free(), "--ofrep-port", free(), "--sync-port", free(),
		}},
		{"unreadable config file", []string{"--config", filepath.Join(tmp, "missing.yaml"), "version"}},
		{"invalid config file", []string{"--config", invalidYAML, "version"}},
	}
	for _, f := range failures {
		r := execute(t, bin, f.args...)
		fmt.Fprintf(&out, "== failure: %s\nexit %d\nfirst error: %s\n",
			f.name, r.exit, normalizeOutput(firstError(r), tmp))
		if strings.HasSuffix(f.name, "config file") {
			// a configuration file that cannot be read is ignored: the command runs as without it
			fmt.Fprintf(&out, "stdout:\n%s", r.stdout)
		}
	}

	if *updateCLI {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(cliGolden, []byte(out.String()), 0o600))
		return
	}
	want, err := os.ReadFile(cliGolden)
	require.NoError(t, err, "record the current binary with `go test -run TestCLICharacterization -update-cli`")
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if got := stamped(out.String()); got != stamped(string(want)) {
		wantLines, gotLines := strings.Split(stamped(string(want)), "\n"), strings.Split(got, "\n")
		for n := 0; n < len(wantLines) && n < len(gotLines); n++ {
			if wantLines[n] != gotLines[n] {
				t.Fatalf("the command line changed at line %d\nwant: %s\n got: %s", n+1, wantLines[n], gotLines[n])
			}
		}
		t.Fatalf("the command line changed: %d lines recorded, %d produced", len(wantLines), len(gotLines))
	}
}
