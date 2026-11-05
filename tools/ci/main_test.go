package main

import (
	"bytes"
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

// The CI tooling builds with GOWORK=off from its own go.mod, so it must not need any module of this repository.
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
}
