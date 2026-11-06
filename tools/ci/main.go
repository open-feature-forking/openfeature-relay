// Command relay-ci holds the repository's CI helpers as subcommands of one binary.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/allocs"
	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/passsets"
	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/testfailures"
)

const usage = `usage: relay-ci <subcommand> [arguments]

The subcommands are added one per helper; there are none yet.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches on the first argument and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, _ = io.WriteString(stdout, usage)
		return 0
	case "compare-allocs":
		return allocs.Run(args[1:], stdout, stderr)
	case "compare-pass-sets":
		return passsets.Run(args[1:], stdout, stderr)
	case "test-failures":
		return testfailures.Run(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "relay-ci: unknown subcommand %q\n\n%s", args[0], usage)
		return 2
	}
}
