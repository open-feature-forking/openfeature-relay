package main

import (
	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/runtime/cmd"
)

// distributionName is the name flagd-full has in its manifest.
const distributionName = "flagd-full"

var (
	version = "dev"
	commit  = "HEAD"
	date    = "unknown"
)

func main() {
	cmd.Execute(distribution.Info{Name: distributionName, Version: version, Commit: commit, Date: date})
}
