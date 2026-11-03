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

// officialComponents are the components of flagd-full: the official ones with the OpenTelemetry telemetry provider,
// which the command configures from the telemetry flags.
func officialComponents() distribution.Components {
	official := components.Official()
	official.Telemetry = otelprovider.NewFactory()
	return official
}

func main() {
	cmd.Execute(distribution.Info{Name: distributionName, Version: version, Commit: commit, Date: date},
		officialComponents(), components.Inventory())
}
