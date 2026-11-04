package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

var timestamps = regexp.MustCompile(`\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}|\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+[+-]\d{4}`)

// versionLine carries the stamps of the build, which a binary the Builder made has of its manifest.
var versionLine = regexp.MustCompile(`(?m)^.*	info	cmd/start.go:\d+	flagd version	.*$`)

// goldenRun compares what a run printed with its recorded exit status, standard output and standard error.
func goldenRun(t *testing.T, golden string, r run, tmp string) {
	t.Helper()
	normalize := func(s string) string { return timestamps.ReplaceAllString(versionLine.ReplaceAllString(normalizeOutput(s, tmp), "<flagd version line>"), "<time>") }
	got := fmt.Sprintf("exit %d\n-- stdout\n%s-- stderr\n%s", r.exit, normalize(r.stdout), normalize(r.stderr))
	path := filepath.Join("testdata", golden)
	if *updateCLI {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "record the current binary with `go test -run %s -update-cli`", t.Name())
	require.Equal(t, string(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))), got)
}

// TestUnknownCommandExit pins what an unknown command prints and the exit status.
func TestUnknownCommandExit(t *testing.T) {
	tmp := t.TempDir()
	bin := buildFlagd(t, tmp)

	r := execute(t, bin, "no-such-command")
	require.Equal(t, 1, r.exit)
	require.Empty(t, r.stdout, "the error goes to standard error")
	goldenRun(t, "unknown_command.golden", r, tmp)
}

// TestUnknownSchemeStartupError pins the text and the exit status of a source with a scheme no sync provider handles.
// The error stays the same when UC-010 lists the schemes of a distribution in it (Q7).
func TestUnknownSchemeStartupError(t *testing.T) {
	tmp := t.TempDir()
	bin := buildFlagd(t, tmp)

	r := execute(t, bin, "start", "--uri", "foo://x")
	require.Equal(t, 1, r.exit)
	goldenRun(t, "unknown_scheme.golden", r, tmp)
}
