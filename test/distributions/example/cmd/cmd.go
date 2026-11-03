// Package cmd is the command line of the example distribution: a distribution brings its own flags instead of flagd's
// (UC-001, the cmd entry of the manifest). It reads three flags, hands the example endpoint its settings as plain data
// and runs the runtime on the components the Builder generated.
//
//	example start --uri <flag file> [--port 8090] [--management-port 8014]
//	example version
package cmd

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/runtime"
)

// EndpointType is the type of the example endpoint, whose settings this command knows.
const EndpointType = "rest-example"

// Execute runs the command line of the distribution; it has the signature of runtime/cmd's.
func Execute(info distribution.Info, components distribution.Components, _ distribution.Inventory) {
	os.Exit(run(os.Args[1:], info, components))
}

func run(args []string, info distribution.Info, components distribution.Components) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: example start --uri <flag file> | example version")
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Printf("%s %s (%s), built at: %s\n", info.Name, info.Version, info.Commit, info.Date)
		return 0
	case "start":
		return start(args[1:], info, components)
	}
	fmt.Fprintf(os.Stderr, "unknown command %q\n", args[0])
	return 2
}

func start(args []string, info distribution.Info, components distribution.Components) int {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	uri := fs.String("uri", "", "the flag file to read (mandatory)")
	port := fs.Int("port", 8090, "the port of the example endpoint")
	management := fs.Uint("management-port", 8014, "the port of the readiness probe")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *uri == "" {
		fmt.Fprintln(os.Stderr, "--uri is mandatory")
		return 2
	}

	rt, err := runtime.FromConfig(logger.NewLogger(nil, false), info.Version, runtime.Config{
		ManagementPort: uint16(*management),
		StreamDeadline: 5 * time.Second,
		SyncProviders:  []sync.SourceConfig{{URI: *uri, Provider: "file"}},
		EndpointSettings: map[string]map[string]any{
			EndpointType: {"port": *port, "formats": []string{"flagd"}, "sources": []string{*uri}},
		},
	}, components)
	if err != nil {
		fmt.Fprintln(os.Stderr, "example:", err)
		return 1
	}
	if err := rt.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "example:", err)
		return 1
	}
	return 0
}
