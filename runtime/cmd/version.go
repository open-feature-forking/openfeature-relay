package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/fips"
	"github.com/spf13/cobra"
)

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of flagd",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		if Version == "dev" {
			details, ok := debug.ReadBuildInfo()
			if ok && details.Main.Version != "" && details.Main.Version != "(devel)" {
				Version = details.Main.Version
				for _, i := range details.Settings {
					if i.Key == "vcs.time" {
						Date = i.Value
					}
					if i.Key == "vcs.revision" {
						Commit = i.Value
					}
				}
			}
		}
		fmt.Printf("flagd: %s (%s), built at: %s\n", Version, Commit, Date)
		fmt.Printf("build variant: %s\n", fips.Variant)
		fmt.Printf("FIPS 140-3 mode: %s\n", fips.Current())
		// The distribution is opt-in: flagd version prints today's lines and no more unless asked (UC-001 Q10).
		if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
			details, _ := debug.ReadBuildInfo()
			fmt.Print(identityLines(distributionIdentity(), details))
		}
	},
}

// runtimeModule is the module path of the runtime, which holds this command.
const runtimeModule = "github.com/open-feature-forking/openfeature-relay/runtime"

// runtimeVersion is the version of the runtime module that was linked, or "dev" when the build replaced it with a
// local directory or knows no version of it.
func runtimeVersion(info *debug.BuildInfo) string {
	if info == nil {
		return "dev"
	}
	for _, dep := range info.Deps {
		if dep.Path != runtimeModule {
			continue
		}
		if dep.Replace != nil || dep.Version == "" || dep.Version == "(devel)" {
			return "dev"
		}
		return dep.Version
	}
	return "dev"
}

func init() {
	versionCmd.Flags().Bool("verbose", false, "also print the distribution and the runtime version")
}
