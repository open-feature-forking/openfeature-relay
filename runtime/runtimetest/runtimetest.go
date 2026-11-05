// Package runtimetest assembles the components of flagd-full that the runtime module can reach without importing the
// flagd module: that module requires the runtime, so a runtime that required it back could not be released. It is for
// the runtime's tests. The event collector requires the runtime and is left out; flagd's generated components test
// pins the full list.
package runtimetest

import (
	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	flageval "github.com/open-feature-forking/openfeature-relay/service/flag-evaluation"
	flagsync "github.com/open-feature-forking/openfeature-relay/service/flag-sync"
	ofrep "github.com/open-feature-forking/openfeature-relay/service/ofrep"
	"github.com/open-feature-forking/openfeature-relay/sync/builder/official"
	otel "github.com/open-feature-forking/openfeature-relay/telemetry/otel"
)

// Components returns flagd-full's factories except those of the modules that require the runtime.
func Components() distribution.Components {
	var c distribution.Components
	c.Sync = official.Factories()
	c.ConfigurationFormats = append(c.ConfigurationFormats, flagdformat.NewFactory())
	c.Evaluators = append(c.Evaluators, flagdeval.NewFactory())
	c.Endpoints = append(c.Endpoints, flageval.NewFactory(), ofrep.NewFactory(), flagsync.NewFactory())
	c.Telemetry = otel.NewFactory()
	return c
}
