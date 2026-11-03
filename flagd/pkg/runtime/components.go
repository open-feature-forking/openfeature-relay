package runtime

import (
	"net/http"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	flageval "github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation"
	"github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation/ofrep"
	flagsync "github.com/open-feature/flagd/flagd/pkg/service/flag-sync"
)

// Components are the factories a distribution passes to the runtime; Config keeps the operator's settings.
// The official sync providers, flagd's configuration format and flagd's evaluator are always there: these are
// the components a distribution adds to them.
type Components struct {
	// Sync are the sync components, besides the official ones, that sources may select.
	Sync []sync.Factory
	// ConfigurationFormats are the formats, besides flagd's own, that sources may name.
	ConfigurationFormats []configformat.Factory
	// Evaluators are the evaluators, besides flagd's own, that resolve the flags of a configuration format.
	// Each one's format must be flagd's own or one of the distribution's configuration formats.
	Evaluators []evaluator.Factory
	// Endpoints are the service endpoints the runtime serves.
	Endpoints []service.Factory
	// Telemetry creates the telemetry provider the runtime, the sync providers and the service endpoints record
	// through. Nil leaves the runtime to build the OpenTelemetry providers it always has from Config.
	Telemetry telemetry.Factory
	// TelemetryConfig is the configuration of Telemetry, as the keys of telemetry.Settings.Config.
	TelemetryConfig map[string]any
	// Middleware are the HTTP middleware of the service endpoints that serve HTTP, in list order: the first sees a request
	// first. They run inside h2c and CORS, which stay layers of the endpoints. flagd-full lists none.
	Middleware []middleware.Configured
	// EventSinks are the event sinks the runtime delivers events to. Each one subscribes to the event bus for the kinds
	// its factory declares. flagd-full lists none, and then nothing subscribes.
	EventSinks []eventsink.Configured
	// MetricsHandler serves /metrics on the management port. Nil leaves it to the telemetry provider, and answers
	// 404 if the provider serves none.
	MetricsHandler http.Handler
}

// Official returns the components of flagd-full: its service endpoints. flagd's configuration format, evaluator and
// the official sync providers are built in.
func Official() Components {
	return Components{Endpoints: officialEndpoints()}
}

// officialEndpoints are the three service endpoints of flagd-full.
func officialEndpoints() []service.Factory {
	return []service.Factory{flageval.Factory{}, ofrep.Factory{}, flagsync.Factory{}}
}
