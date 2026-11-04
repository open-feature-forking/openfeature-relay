package cmd

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/fips"
)

// captureStdout returns what f writes to os.Stdout.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = original }()

	done := make(chan string)
	go func() {
		out, _ := io.ReadAll(r)
		done <- string(out)
	}()
	f()
	require.NoError(t, w.Close())
	return <-done
}

// TestVersionOutputCharacterization pins the output of `flagd version` byte for byte: the release version, the
// short commit and the build date as the release build stamps them (main.version, main.commit, main.date), and the
// build variant. A flagd-full built by the Builder keeps it (UC-001 drop-in rule). The variant and the FIPS 140-3
// mode depend on how the test binary is built (make test-fips builds the FIPS variant), so their two lines are
// taken from the fips package; the standard build's text is pinned by the golden of the flagd binary.
func TestVersionOutputCharacterization(t *testing.T) {
	version, commit, date := Version, Commit, Date
	variant := "build variant: " + fips.Variant + "\nFIPS 140-3 mode: " + fips.Current().String() + "\n"
	t.Cleanup(func() { Version, Commit, Date = version, commit, date })

	tests := map[string]struct {
		version, commit, date string
		want                  string
	}{
		"stamped by the release build": {
			"v0.15.3", "a1b2c3d", "2026-01-02T03:04:05Z",
			"flagd: v0.15.3 (a1b2c3d), built at: 2026-01-02T03:04:05Z\n" + variant,
		},
		// A test binary has no module version, so "dev" is printed with the defaults of main.
		"unstamped": {
			"dev", "HEAD", "unknown",
			"flagd: dev (HEAD), built at: unknown\n" + variant,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			Version, Commit, Date = tt.version, tt.commit, tt.date
			got := captureStdout(t, func() { versionCmd.Run(versionCmd, nil) })
			require.Equal(t, tt.want, got)
		})
	}
}

// The version command is the one that prints the version, with the short text the help lists.
func TestVersionCommandDeclaration(t *testing.T) {
	require.Equal(t, "version", versionCmd.Use)
	require.Equal(t, "Print the version number of flagd", versionCmd.Short)
}
