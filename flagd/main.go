package main

import (
	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/runtime/cmd"
	otelprovider "github.com/open-feature-forking/openfeature-relay/telemetry/otel"
	"github.com/open-feature/flagd/flagd/components"
)

// distributionName is the name flagd-full has in its manifest.
const distributionName = "flagd-full"

var (
	version = "dev"
	commit  = "HEAD"
	date    = "unknown"
)

func main() {
	official := components.Official()
	// the telemetry provider is the command's choice: flagd-full records through OpenTelemetry
	official.Telemetry = otelprovider.NewFactory()
	cmd.Execute(distribution.Info{Name: distributionName, Version: version, Commit: commit, Date: date},
		official, components.Inventory())
}
