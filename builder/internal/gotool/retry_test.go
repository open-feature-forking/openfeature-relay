//go:build unix

package gotool

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeGo writes a go binary that counts its calls in calls and fails the first failures of them with output.
func fakeGo(t *testing.T, failures int, output string) (Tool, string) {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := "#!/bin/sh\n" +
		"n=$(cat " + calls + " 2>/dev/null || echo 0); n=$((n+1)); echo $n > " + calls + "\n" +
		"if [ $n -le " + strconv.Itoa(failures) + " ]; then printf '%s\\n' '" + output + "' >&2; exit 1; fi\n"
	bin := filepath.Join(dir, "go")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil { //nolint:gosec // an executable test script
		t.Fatal(err)
	}
	return Tool{Go: bin, Env: os.Environ()}, calls
}

func callCount(t *testing.T, calls string) string {
	t.Helper()
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func noDelays(t *testing.T) {
	t.Helper()
	saved := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { retryDelays = saved })
}

// TestTidyRetriesANetworkFailure pins that a call the module proxy dropped runs again, at most three times in all.
func TestTidyRetriesANetworkFailure(t *testing.T) {
	noDelays(t)
	const dropped = `go: golang.org/x/tools@v0.41.0: read "https://proxy.golang.org/golang.org/x/tools/@v/v0.41.0.mod": stream error: stream ID 1721; INTERNAL_ERROR; received from peer`

	tool, calls := fakeGo(t, 2, dropped)
	if err := tool.Tidy(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatalf("two dropped requests, then success: %v", err)
	}
	if got := callCount(t, calls); got != "3" {
		t.Fatalf("calls = %s, want 3", got)
	}

	tool, calls = fakeGo(t, 3, dropped)
	if err := tool.Tidy(context.Background(), t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "INTERNAL_ERROR") {
		t.Fatalf("three dropped requests end with the last one's error, got %v", err)
	}
	if got := callCount(t, calls); got != "3" {
		t.Fatalf("calls = %s, want 3", got)
	}
}

// TestOtherFailuresAreNotRepeated pins that a failure that is not the network's, a compile error or a module the
// proxy does not have, fails at once.
func TestOtherFailuresAreNotRepeated(t *testing.T) {
	noDelays(t)
	for _, output := range []string{
		`# example.test/dist` + "\n" + `./main.go:3:2: undefined: comp.NewFactory`,
		`go: example.test/missing@v1.0.0: reading https://proxy.golang.org/example.test/missing/@v/v1.0.0.mod: 404 Not Found`,
	} {
		tool, calls := fakeGo(t, 1, output)
		if err := tool.Build(context.Background(), t.TempDir(), "out", "", nil); err == nil {
			t.Fatalf("%q: want the error", output)
		}
		if got := callCount(t, calls); got != "1" {
			t.Fatalf("%q: calls = %s, want 1", output, got)
		}
	}
}
