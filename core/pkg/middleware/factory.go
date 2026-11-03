package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// Middleware wraps the HTTP handler of every service endpoint that serves over HTTP. For each request it either
// passes the request on to the wrapped handler or answers it itself, never both.
type Middleware interface {
	Handler(next http.Handler) http.Handler
}

// Factory creates a middleware.
type Factory interface {
	Type() string
	// Create builds the middleware from settings. An invalid configuration is an error that names the faulty
	// value; the distribution then does not start.
	Create(settings Settings) (Middleware, error)
}

// Settings are what a factory gets to build a middleware.
type Settings struct {
	Logger *logger.Logger
	// Telemetry is the distribution's telemetry provider, the only way a middleware records traces and metrics.
	// It is never nil.
	Telemetry telemetry.Provider
	Config    map[string]any
}

// Configured is a middleware factory with the configuration a distribution creates it from.
type Configured struct {
	Factory Factory
	Config  map[string]any
}

// Chain wraps h with the middleware in order: the first middleware sees a request first. Without middleware it
// returns h.
func Chain(mws []Middleware, h http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i].Handler(h)
	}
	return h
}

// ValidateDeclaration checks what a factory declares: a non-empty type name.
func ValidateDeclaration(f Factory) error {
	if f == nil {
		return errors.New("the factory is nil")
	}
	if strings.TrimSpace(f.Type()) == "" {
		return errors.New("the type name is empty")
	}
	return nil
}
