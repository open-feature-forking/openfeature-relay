// Command relay-builder turns a manifest into a distribution of the relay: a flagd binary that contains exactly the
// components the manifest selects (UC-001).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/open-feature-forking/openfeature-relay/builder/internal/build"
	"github.com/open-feature-forking/openfeature-relay/builder/internal/generate"
	"github.com/open-feature-forking/openfeature-relay/builder/internal/gotool"
	"github.com/open-feature-forking/openfeature-relay/builder/internal/manifest"
)

// Exit codes.
const (
	exitOK       = 0
	exitInvalid  = 1
	exitUsage    = 2
	exitInternal = 3
	exitFailed   = 4
)

// options are the flags of the command.
type options struct {
	manifest string
	// The steps the Builder can skip (BR-008), and the relaxation of the version check.
	skipGenerate, skipGetModules, skipCompile, skipStrictVersioning bool
	goBinary                                                        string
	verbose                                                         bool
	// validateOnly stops after the manifest is checked.
	validateOnly bool
	// What the build stamps into the binary, and where the source goes, when they are not the manifest's and the
	// Builder's own defaults. Each is passed on as given.
	distVersion, commit, date, output string
	// probeOutput is a file the output of the probe is written to.
	probeOutput string
}

func parseFlags(args []string, stderr io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("relay-builder", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.manifest, "manifest", "", "path of the manifest `file` (mandatory)")
	fs.BoolVar(&o.skipGenerate, "skip-generate", false, "do not generate the distribution source; build the source already in the output location")
	fs.BoolVar(&o.skipGetModules, "skip-get-modules", false, "do not resolve the component modules; use the dependencies recorded with the source")
	fs.BoolVar(&o.skipCompile, "skip-compile", false, "do not compile; leave the source in the output location")
	fs.BoolVar(&o.skipStrictVersioning, "skip-strict-versioning", false, "report a resolved version that differs from the declared one as a warning, not a failure")
	fs.StringVar(&o.goBinary, "go", "go", "the go `binary` to run")
	fs.BoolVar(&o.verbose, "verbose", false, "print what the Builder does")
	fs.BoolVar(&o.validateOnly, "validate-only", false, "check the manifest and stop")
	fs.StringVar(&o.distVersion, "dist-version", "", "stamp this `version` into the binary instead of the manifest's dist.version")
	fs.StringVar(&o.commit, "commit", "", "stamp this source `revision` into the binary instead of the short revision of the manifest's directory")
	fs.StringVar(&o.date, "date", "", "stamp this build `date` into the binary instead of the current time (RFC 3339, UTC)")
	fs.StringVar(&o.output, "output", "", "write the source to this `directory` instead of the manifest's output_path")
	fs.StringVar(&o.probeOutput, "probe-output", "", "write what the probe printed, the factories of the components with their schemes and needs, to this `file`")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: relay-builder --manifest <file> [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if o.manifest == "" {
		return o, fmt.Errorf("--manifest is mandatory")
	}
	return o, nil
}

// run is the command; it returns the exit code.
func run(args []string, stdout, stderr io.Writer) int {
	o, err := parseFlags(args, stderr)
	if err != nil {
		if err != flag.ErrHelp {
			fmt.Fprintf(stderr, "relay-builder: %v\n", err)
		}
		if err == flag.ErrHelp {
			return exitOK
		}
		return exitUsage
	}

	m, err := manifest.Load(o.manifest)
	if err != nil {
		var invalid *manifest.Error
		if errors.As(err, &invalid) {
			fmt.Fprintf(stderr, "%v\n", invalid)
			return exitInvalid
		}
		fmt.Fprintf(stderr, "relay-builder: %v\n", err)
		return exitInternal
	}
	if o.validateOnly {
		fmt.Fprintf(stdout, "the manifest %s is valid: %s %s, %d component(s)\n", o.manifest, m.Dist.Name, m.Dist.Version, len(m.Entries()))
		return exitOK
	}
	return buildDistribution(m, o, stdout, stderr)
}

// version is the version of the Builder; a release build stamps it with -ldflags "-X main.version=v1.2.3". It is the
// version of the core and runtime modules that a manifest does not pin.
var version = "dev"

// buildOptions turns the flags into the options of the build.
func buildOptions(o options, stderr io.Writer) build.Options {
	generateOptions := generate.Options{OutputDir: o.output}
	if version != "dev" {
		generateOptions.RuntimeVersion, generateOptions.CoreVersion = version, version
	}
	tool := gotool.Tool{Go: o.goBinary}
	if o.verbose {
		tool.Log = stderr
	}
	return build.Options{
		SkipGenerate: o.skipGenerate, SkipGetModules: o.skipGetModules, SkipCompile: o.skipCompile,
		SkipStrictVersioning: o.skipStrictVersioning, Generate: generateOptions, Tool: tool, Warn: stderr,
		DistVersion: o.distVersion, Commit: o.commit, Date: o.date, ProbeOutput: o.probeOutput,
	}
}

func buildDistribution(m *manifest.Manifest, o options, stdout, stderr io.Writer) int {
	res, err := build.Run(context.Background(), m, buildOptions(o, stderr))
	if err != nil {
		fmt.Fprintf(stderr, "relay-builder: %v\n", err)
		return exitFailed
	}
	if res.Binary != "" {
		fmt.Fprintf(stdout, "binary: %s\n", res.Binary)
	}
	fmt.Fprintf(stdout, "source: %s\n", res.SourceDir)
	return exitOK
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
