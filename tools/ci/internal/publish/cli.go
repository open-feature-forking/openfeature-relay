package publish

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/flagargs"
	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/ghactions"
)

const usage = `usage: relay-ci publish <command> [arguments]

  push <layouts directory> --short <7 hex> [--names n1,n2] [--outputs <file>]
      Pushes every layout by digest and under its sha-<short> tag. It never pushes a main tag: move-main does.
  move-main --short <7 hex> --sha <full commit> [--names n1,n2] [--force]
      Moves the main tags to the sha-<short> images, only if the commit :main points at is an ancestor of --sha.
      --force moves it anyway for a commit this checkout lacks, and for no other.

The destinations are the seven images under ghcr.io/open-feature-forking/ and nothing else. REGISTRY_HOST may name a
local registry; credentials come from REGISTRY_USER and REGISTRY_TOKEN.
`

var shortPattern = regexp.MustCompile(`^[0-9a-f]{7}$`)

func splitNames(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// Run runs one `relay-ci publish` command and returns the exit code: 3 when a destination is refused.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	var spec flagargs.Spec
	switch args[0] {
	case "push":
		spec = flagargs.Spec{Values: []string{"short", "names", "outputs"}, Required: []string{"short"}, Positionals: 1}
	case "move-main":
		spec = flagargs.Spec{Values: []string{"short", "sha", "names"}, Bools: []string{"force"}, Required: []string{"short", "sha"}}
	case "help", "-h", "--help":
		_, _ = io.WriteString(stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "relay-ci publish: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	parsed, err := flagargs.Parse(args[1:], spec)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "relay-ci publish %s: %v\n", args[0], err)
		return 2
	}
	if !shortPattern.MatchString(parsed.Value("short")) {
		_, _ = io.WriteString(stderr, "--short is seven hex digits\n")
		return 1
	}
	log := func(line string) { _, _ = fmt.Fprintln(stdout, line) }
	if args[0] == "push" {
		err = Push(PushOptions{Layouts: parsed.Positional[0], Short: parsed.Value("short"),
			Names: splitNames(parsed.Value("names")), Outputs: parsed.Value("outputs")}, log)
	} else {
		err = MoveMain(MoveOptions{Short: parsed.Value("short"), Sha: parsed.Value("sha"),
			Names: splitNames(parsed.Value("names")), Force: parsed.Bool("force")}, log)
	}
	var refused *Refused
	switch {
	case err == nil:
		return 0
	case errors.As(err, &refused):
		_, _ = fmt.Fprintln(stderr, "::error::refused: "+ghactions.EscapeData(refused.Message))
		return 3
	default:
		_, _ = fmt.Fprintf(stderr, "relay-ci publish %s: %v\n", args[0], err)
		return 1
	}
}
