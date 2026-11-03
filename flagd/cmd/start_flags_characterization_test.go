package cmd

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

var updateStartFlags = flag.Bool("update-start-flags", false, "rewrite testdata/start_flags.golden from the current flags")

const startFlagsGolden = "testdata/start_flags.golden"

func envName(name string) string {
	return "FLAGD_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// allFlags are the flags `flagd start` accepts: its own and the persistent ones of the root command.
func allFlags() []*pflag.Flag {
	var flags []*pflag.Flag
	seen := map[string]bool{}
	for _, set := range []*pflag.FlagSet{startCmd.Flags(), startCmd.InheritedFlags()} {
		set.VisitAll(func(f *pflag.Flag) {
			if !seen[f.Name] {
				seen[f.Name] = true
				flags = append(flags, f)
			}
		})
	}
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
	return flags
}

// TestStartFlagsCharacterization pins every flag of `flagd start`: its name, shorthand, type, default, usage text,
// and the environment variable that sets it. A flagd-full built by the Builder keeps all of them (UC-001 Q15 and the
// drop-in rule).
func TestStartFlagsCharacterization(t *testing.T) {
	var out strings.Builder
	for _, f := range allFlags() {
		fmt.Fprintf(&out, "--%s\n", f.Name)
		fmt.Fprintf(&out, "  shorthand: %q\n", f.Shorthand)
		fmt.Fprintf(&out, "  type: %s\n", f.Value.Type())
		fmt.Fprintf(&out, "  default: %q\n", f.DefValue)
		fmt.Fprintf(&out, "  env: %s\n", envName(f.Name))
		fmt.Fprintf(&out, "  hidden: %v\n", f.Hidden)
		fmt.Fprintf(&out, "  deprecated: %q\n", f.Deprecated)
		fmt.Fprintf(&out, "  usage: %s\n", f.Usage)
	}

	if *updateStartFlags {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(startFlagsGolden, []byte(out.String()), 0o600))
		return
	}
	want, err := os.ReadFile(startFlagsGolden)
	require.NoError(t, err, "record the current flags with `go test -run TestStartFlagsCharacterization -update-start-flags`")
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	require.Equal(t, string(want), out.String())
}

// Every flag is read from the environment as FLAGD_<NAME> and from a configuration file under its own name, which is
// the key `flagd start` reads it by.
func TestStartFlagsAreReadFromEnvironmentAndConfigFile(t *testing.T) {
	viper.AutomaticEnv()
	t.Cleanup(func() {
		viper.SetConfigType("yaml")
		_ = viper.ReadConfig(strings.NewReader(""))
	})

	var config strings.Builder
	for _, f := range allFlags() {
		t.Setenv(envName(f.Name), "from-env-"+f.Name)
		fmt.Fprintf(&config, "%s: from-file-%s\n", f.Name, f.Name)
	}
	for _, f := range allFlags() {
		require.Equal(t, "from-env-"+f.Name, viper.GetString(f.Name), "environment variable %s", envName(f.Name))
	}

	for _, f := range allFlags() {
		require.NoError(t, os.Unsetenv(envName(f.Name)))
	}
	path := filepath.Join(t.TempDir(), "flagd.yaml")
	require.NoError(t, os.WriteFile(path, []byte(config.String()), 0o600))
	viper.SetConfigFile(path)
	require.NoError(t, viper.ReadInConfig())
	for _, f := range allFlags() {
		if !f.Changed {
			require.Equal(t, "from-file-"+f.Name, viper.GetString(f.Name), "configuration file key %s", f.Name)
		}
	}
}

// The subcommands of the root command.
func TestRootSubcommands(t *testing.T) {
	var names []string
	for _, c := range rootCmd.Commands() {
		names = append(names, c.Name())
	}
	sort.Strings(names)
	require.Equal(t, []string{"start", "version"}, names, "completion and help are added when the command executes")
}
