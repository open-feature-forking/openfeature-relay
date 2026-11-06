package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/textio"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "no arguments", wantCode: 2, wantStderr: "usage: relay-ci"},
		{name: "unknown subcommand", args: []string{"nope"}, wantCode: 2, wantStderr: `unknown subcommand "nope"`},
		{name: "help", args: []string{"help"}, wantStdout: "usage: relay-ci"},
		{name: "dash h", args: []string{"-h"}, wantStdout: "usage: relay-ci"},
		{name: "compare-allocs without arguments", args: []string{"compare-allocs"}, wantCode: 2, wantStderr: "relay-ci compare-allocs <base> <head>"},
		{name: "compare-pass-sets without arguments", args: []string{"compare-pass-sets"}, wantCode: 2, wantStdout: "relay-ci compare-pass-sets"},
		{name: "dash dash help", args: []string{"--help"}, wantStdout: "usage: relay-ci"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(tt.args, &stdout, &stderr); got != tt.wantCode {
				t.Errorf("exit code = %d, want %d", got, tt.wantCode)
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

// The CI tooling builds with GOWORK=off from its own go.mod, so it must not need any module of this repository. In
// workspace mode an import of a repository package would resolve; outside it, it does not, and go list fails.
func TestRequiresNoRelayModule(t *testing.T) {
	text, err := textio.ReadText("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range textio.SplitLines(text) {
		if strings.HasPrefix(strings.TrimSpace(line), "module ") {
			continue
		}
		if strings.Contains(line, "open-feature-forking/openfeature-relay") || strings.Contains(line, "open-feature/flagd") {
			t.Errorf("go.mod refers to a module of this repository: %q", line)
		}
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command on the PATH")
	}
	cmd := exec.Command(goBin, "list", "-deps", "-test", "-f", "{{with .Module}}{{.Path}}{{end}}", "./...")
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("GOWORK=off go list -deps ./...: %v\n%s", err, stderr.String())
	}
	for _, mod := range strings.Fields(string(out)) {
		own := "github.com/open-feature-forking/openfeature-relay/tools/ci"
		if mod != own && (strings.HasPrefix(mod, "github.com/open-feature-forking/openfeature-relay") || strings.HasPrefix(mod, "github.com/open-feature/flagd")) {
			t.Errorf("tools/ci depends on the module %s of this repository", mod)
		}
	}
}
