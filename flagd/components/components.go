// Package components lists the components of flagd-full: the distribution this module's main package builds. A
// distribution made by the Builder has a package of the same shape.
package components

import (
	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	"github.com/open-feature-forking/openfeature-relay/sync/builder/official"
	flageval "github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation"
	"github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation/ofrep"
	flagsync "github.com/open-feature/flagd/flagd/pkg/service/flag-sync"
)

// Official returns the components of flagd-full: the official sync components with their fetch triggers, flagd's
// configuration format and evaluator, and the three service endpoints. The telemetry provider is the command's choice.
func Official() distribution.Components {
	return distribution.Components{
		Sync:                 official.Factories(),
		ConfigurationFormats: []configformat.Factory{flagdformat.NewFactory()},
		Evaluators:           []evaluator.Factory{flagdeval.NewFactory()},
		Endpoints:            Endpoints(),
	}
}

// Endpoints are the three service endpoints of flagd-full.
func Endpoints() []service.Factory {
	return []service.Factory{flageval.Factory{}, ofrep.Factory{}, flagsync.Factory{}}
}
