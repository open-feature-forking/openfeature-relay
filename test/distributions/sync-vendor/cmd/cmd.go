// Package cmd is the command line of the sync-vendor test distribution, a vendor configuration proxy (UC-017 A7): it
// reads the two sources, a flagd flag file and a keyvalue file, and the ports of its endpoints, hands the example
// endpoint its settings as plain data and runs the runtime on the components the Builder generated.
//
//	sync-vendor start --uri <flagd file> --vendor-uri <keyvalue file> [--sync-port 8015] [--port 8090] [--management-port 8014]
//	sync-vendor version
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

// Types of the vendor configuration format and of the example endpoint, whose settings this command knows.
const (
	FormatType   = "keyvalue"
	EndpointType = "rest-example"
)

// Execute runs the command line of the distribution; it has the signature of runtime/cmd's.
func Execute(info distribution.Info, components distribution.Components, _ distribution.Inventory) {
	os.Exit(run(os.Args[1:], info, components))
}

func run(args []string, info distribution.Info, components distribution.Components) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: sync-vendor start --uri <flagd file> --vendor-uri <keyvalue file> | sync-vendor version")
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
	uri := fs.String("uri", "", "the flagd flag file to read")
	vendorURI := fs.String("vendor-uri", "", "the keyvalue file to read")
	syncPort := fs.Uint("sync-port", 8015, "the port of the flag sync endpoint")
	port := fs.Int("port", 8090, "the port of the example endpoint")
	management := fs.Uint("management-port", 8014, "the port of the readiness probe")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *uri == "" && *vendorURI == "" {
		fmt.Fprintln(os.Stderr, "--uri or --vendor-uri is mandatory")
		return 2
	}

	var sources []sync.SourceConfig
	if *uri != "" {
		sources = append(sources, sync.SourceConfig{URI: *uri, Provider: "file"})
	}
	var vendorSources []string
	if *vendorURI != "" {
		sources = append(sources, sync.SourceConfig{URI: *vendorURI, Provider: "file", Format: FormatType})
		vendorSources = []string{*vendorURI}
	}

	rt, err := runtime.FromConfig(logger.NewLogger(nil, false), info.Version, runtime.Config{
		ManagementPort:  uint16(*management),
		SyncServicePort: uint16(*syncPort),
		StreamDeadline:  5 * time.Second,
		SyncProviders:   sources,
		EndpointSettings: map[string]map[string]any{
			EndpointType: {"port": *port, "formats": []string{FormatType}, "sources": vendorSources},
		},
	}, components)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sync-vendor:", err)
		return 1
	}
	if err := rt.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "sync-vendor:", err)
		return 1
	}
	return 0
}
