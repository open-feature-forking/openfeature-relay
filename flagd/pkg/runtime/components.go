package runtime

import (
	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	flageval "github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation"
	"github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation/ofrep"
	flagsync "github.com/open-feature/flagd/flagd/pkg/service/flag-sync"
)

// Components are the factories a distribution passes to the runtime; Config keeps the operator's settings.
// The official sync providers, flagd's configuration format and flagd's evaluator are always there: these are
// the components a distribution adds to them.
type Components = distribution.Components

// Official returns the components of flagd-full: its service endpoints. flagd's configuration format, evaluator and
// the official sync providers are built in.
func Official() Components {
	return Components{Endpoints: officialEndpoints()}
}

// officialEndpoints are the three service endpoints of flagd-full.
func officialEndpoints() []service.Factory {
	return []service.Factory{flageval.Factory{}, ofrep.Factory{}, flagsync.Factory{}}
}
