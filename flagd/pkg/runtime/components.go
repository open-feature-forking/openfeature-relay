package runtime

import (
	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	flageval "github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation"
	"github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation/ofrep"
	flagsync "github.com/open-feature/flagd/flagd/pkg/service/flag-sync"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Components are the factories a distribution passes to the runtime; Config keeps the operator's settings. The
// official sync providers are always there: these are the components a distribution adds to them.
type Components = distribution.Components

// Official returns the components of flagd-full: flagd's configuration format and evaluator, its service
// endpoints, and the metrics of the default Prometheus registry. The official sync providers are still built in.
func Official() Components {
	return Components{
		ConfigurationFormats: []configformat.Factory{flagdformat.NewFactory()},
		Evaluators:           []evaluator.Factory{flagdeval.NewFactory()},
		Endpoints:            officialEndpoints(),
		MetricsHandler:       promhttp.Handler(),
	}
}

// officialEndpoints are the three service endpoints of flagd-full.
func officialEndpoints() []service.Factory {
	return []service.Factory{flageval.Factory{}, ofrep.Factory{}, flagsync.Factory{}}
}
