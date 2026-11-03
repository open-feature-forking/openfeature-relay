// Package distribution holds what a distribution hands its runtime and its commands: the factories of the
// components it contains, the facts its build stamped, and the inventory of what is linked. All of it is plain
// data over core types, so the generated main of a distribution and the runtime exchange nothing else.
package distribution

import (
	"net/http"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
)

// Components are the factories a distribution passes to the runtime; the runtime's Config keeps the operator's
// settings.
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
	// MetricsHandler serves /metrics on the management port. Nil answers 404.
	MetricsHandler http.Handler
}

// Info is what the main of a distribution knows about its build: the values its build stamped in with the linker,
// and the name of the distribution.
type Info struct {
	// Name is the distribution's name, as its manifest gives it.
	Name string
	// Version, Commit and Date are what the build stamped: the release version, the short commit and the build date.
	Version string
	Commit  string
	Date    string
}
