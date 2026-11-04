package cmd

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var updateCLI = flag.Bool("update-cli", false, "rewrite testdata/cli.golden from the current binary")

const cliGolden = "testdata/cli.golden"

var logTimestamp = regexp.MustCompile(`\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}`)

// TestCLICharacterization pins the command line of flagd-proxy: the help it prints and what a command or flag it does
// not have prints and exits with. Step 10 builds other distributions on the runtime and changes no flagd-proxy file
// (UC-017 BR-007), so nothing of it moves.
func TestCLICharacterization(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "flagd-proxy")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", bin, "github.com/open-feature/flagd/flagd-proxy")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building flagd-proxy: %v\n%s", err, out)
	}

	var golden strings.Builder
	for _, args := range [][]string{{"--help"}, {"start", "--help"}, {"version"}, {"start", "--nope"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		cancel()
		exit := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exit = exitErr.ExitCode()
		} else if err != nil {
			t.Fatalf("running flagd-proxy %v: %v", args, err)
		}
		fmt.Fprintf(&golden, "== flagd-proxy %s\nexit %d\n-- stdout\n%s-- stderr\n%s", strings.Join(args, " "), exit, stdout.String(), stderr.String())
	}
	got := logTimestamp.ReplaceAllString(golden.String(), "<time>")

	if *updateCLI {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cliGolden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(cliGolden)
	if err != nil {
		t.Fatalf("record the current binary with `go test -run TestCLICharacterization -update-cli`: %v", err)
	}
	if w := string(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))); w != got {
		t.Errorf("the command line changed\nwant:\n%s\ngot:\n%s", w, got)
	}
}
