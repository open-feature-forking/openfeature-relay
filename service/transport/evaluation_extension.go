package service

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// Events is the part of the host's event subscriptions that an evaluation extension uses: it subscribes a stream to
// change notifications and ends the subscription.
type Events interface {
	Subscribe(ctx context.Context, id any, selector *store.Selector, notifyChan chan service.Notification)
	Unsubscribe(id any)
}

// EvaluationHost is what the flag evaluation endpoint hands an extension it mounts.
type EvaluationHost struct {
	// Logger is the host's logger, with its component field.
	Logger *logger.Logger
	// Resolver is the host's traced resolver, so that the evaluation events keep naming the host.
	Resolver evaluator.IResolver
	// Events is the host's subscriptions: a stream of an extension receives the same notifications as the host's.
	Events Events
	// Telemetry is the telemetry provider of the host; never nil.
	Telemetry telemetry.Provider
	// Configuration is the host's configuration.
	Configuration service.Configuration
	// HandlerOptions is every option the host applies to its own handlers: the telemetry's ConnectRPC instrumentation and
	// the JSON marshal options (falsy fields are emitted, unknown fields are discarded). Treat it as read-only.
	HandlerOptions []connect.HandlerOption
}

// Route is a set of RPC procedures an extension serves under one path prefix.
type Route struct {
	// Prefix is the path prefix of the services, such as "/schema.v1.Service/": it starts and ends with a slash.
	Prefix string
	// Handler serves every request whose path starts with Prefix.
	Handler http.Handler
	// Procedures are the paths of the RPC procedures, such as "/schema.v1.Service/ResolveAll". They bound the route
	// attribute of the HTTP metrics; a path not listed is labelled "/".
	Procedures []string
}

// EvaluationExtension is implemented by a hosted endpoint that the flag evaluation endpoint serves on its own server.
type EvaluationExtension interface {
	// Mount returns the routes the host serves. The host calls it once, when it is created.
	Mount(EvaluationHost) ([]Route, error)
}
