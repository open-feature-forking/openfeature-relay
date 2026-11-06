package service

import (
	"context"
	"net"
	"net/http"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// ListenFunc opens the listener of a network and address, as net.Listen does. A distribution's embedder supplies one to
// decide how every listener of the runtime is opened (Components.Listen); nil means net.Listen.
type ListenFunc func(network, address string) (net.Listener, error)

// Or returns f, or net.Listen when f is nil.
func (f ListenFunc) Or() ListenFunc {
	if f == nil {
		return net.Listen
	}
	return f
}

// Endpoint is a service endpoint: it serves clients from when the distribution starts until it shuts down.
//
// An endpoint that holds a resource from the moment it is created, such as a bound listener, also implements io.Closer.
// The runtime closes it when the distribution fails to build after the endpoint was created, so that Serve never runs;
// Close releases what Create took and is safe to repeat. An endpoint that takes nothing at Create needs no Close.
type Endpoint interface {
	// Serve blocks while the endpoint serves. When ctx ends it ends its clients' connections and returns nil,
	// within the endpoint budget of the runtime's shutdown. An error stops the distribution.
	Serve(ctx context.Context) error
}

// Needs declares what a service endpoint resolves flags with, which decides what the runtime injects.
type Needs struct {
	// Router asks for Settings.Resolver, the typed calls over the evaluation router.
	Router bool
	// Format names a configuration format whose evaluator the endpoint needs itself, for its native results;
	// it asks for Settings.Evaluator. A distribution without that format's evaluator fails to start.
	Format string
}

// Settings is what a distribution hands a service endpoint factory.
type Settings struct {
	// Store holds the documents of every configuration format, with their versions.
	Store *store.Store
	// Bus carries configuration change, evaluation and tracking events.
	Bus *events.Bus
	// Resolver resolves flags for a selector through the evaluation router, and publishes an evaluation
	// event for each of them. It is set only for Needs.Router.
	Resolver evaluator.IResolver
	// Evaluator is the evaluator of Needs.Format. It is set only for Needs.Format. An endpoint that uses it
	// directly publishes the evaluation events itself.
	Evaluator evaluator.Evaluator
	// Initialized is closed once every source has delivered its flag configuration once.
	Initialized <-chan struct{}
	// HTTPMiddleware wraps the HTTP handler of an endpoint that serves HTTP with the middleware the
	// distribution selects. Nil means none; see WrapHTTP.
	HTTPMiddleware func(http.Handler) http.Handler
	Logger         *logger.Logger
	// Telemetry is the telemetry provider the endpoint records spans and metrics through. Nil means the noop
	// telemetry provider; see TelemetryProvider.
	Telemetry telemetry.Provider
	// Formats are the types of the configuration formats of the distribution, flagd's first when it has it.
	Formats []string
	// Sources are the URIs of the sources the runtime reads.
	Sources []string
	// Config holds the endpoint's own settings.
	Config map[string]any
	// Extensions are the hosted endpoints of this factory's type, created by the runtime. Only a factory that is
	// Hosting receives them; see Hosted.
	Extensions []Endpoint
	// Listen opens the endpoint's listeners; see ListenFunc. Nil means net.Listen. An endpoint opens every listener it serves on
	// through it and never calls net.Listen itself (UC-004 BR-012): the testbed's listener gate and embedders that hand over a
	// listener they opened earlier decide how it listens.
	Listen ListenFunc
}

// TelemetryProvider returns Telemetry, or the noop telemetry provider when none is set.
func (s Settings) TelemetryProvider() telemetry.Provider { return telemetry.OrNoop(s.Telemetry) }

// WrapHTTP applies HTTPMiddleware to handler; without middleware it returns handler.
func (s Settings) WrapHTTP(handler http.Handler) http.Handler {
	if s.HTTPMiddleware == nil {
		return handler
	}
	return s.HTTPMiddleware(handler)
}
