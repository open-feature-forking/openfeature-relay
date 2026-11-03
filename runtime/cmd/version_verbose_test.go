package cmd

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVersionVerboseNamesDistribution: --verbose adds the distribution and the runtime after the lines flagd version
// prints without it (UC-001 Q10), which stay byte for byte as TestVersionOutputCharacterization pins them.
func TestVersionVerboseNamesDistribution(t *testing.T) {
	version, commit, date, distribution := Version, Commit, Date, Distribution
	t.Cleanup(func() {
		Version, Commit, Date, Distribution = version, commit, date, distribution
		require.NoError(t, versionCmd.Flags().Set("verbose", "false"))
	})
	Version, Commit, Date, Distribution = "v0.15.3", "a1b2c3d", "2026-01-02T03:04:05Z", "flagd-full"

	plain := captureStdout(t, func() { versionCmd.Run(versionCmd, nil) })
	require.NoError(t, versionCmd.Flags().Set("verbose", "true"))
	verbose := captureStdout(t, func() { versionCmd.Run(versionCmd, nil) })

	require.Equal(t, plain+"distribution: flagd-full v0.15.3\nruntime: dev\n", verbose,
		"a test binary has no runtime module in its build information, which reads as dev")
}

func TestRuntimeVersion(t *testing.T) {
	released := &debug.Module{Path: runtimeModule, Version: "v0.3.1"}
	tests := map[string]struct {
		info *debug.BuildInfo
		want string
	}{
		"no build information":            {nil, "dev"},
		"the runtime is not a dependency": {&debug.BuildInfo{Deps: []*debug.Module{{Path: "example.com/other", Version: "v1.0.0"}}}, "dev"},
		"a released runtime":              {&debug.BuildInfo{Deps: []*debug.Module{released}}, "v0.3.1"},
		"a replaced runtime shows dev": {
			&debug.BuildInfo{Deps: []*debug.Module{{Path: runtimeModule, Version: "v0.0.0", Replace: &debug.Module{Path: "../runtime"}}}}, "dev",
		},
		"a development version":        {&debug.BuildInfo{Deps: []*debug.Module{{Path: runtimeModule, Version: "(devel)"}}}, "dev"},
		"a dependency without version": {&debug.BuildInfo{Deps: []*debug.Module{{Path: runtimeModule}}}, "dev"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tt.want, runtimeVersion(tt.info))
		})
	}
}
