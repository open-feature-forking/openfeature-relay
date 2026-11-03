package ofrep

import (
	"context"
	"fmt"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"go.uber.org/zap"
)

// Type is the type of the OFREP service endpoint.
const Type = "ofrep"

// ConfigKey is the key of Settings.Config under which the runtime hands the endpoint its EndpointConfig.
const ConfigKey = "config"

// EndpointConfig is what the runtime derives from the operator's settings for the endpoint.
type EndpointConfig struct {
	Service                    SvcConfiguration
	CORS                       []string
	ContextValues              map[string]any
	HeaderToContextKeyMappings map[string]string
}

// Factory creates the OFREP service endpoint. It resolves through the evaluation router and publishes nothing
// by hand.
type Factory struct{}

var _ service.Factory = Factory{}

// Type implements service.Factory.
func (Factory) Type() string { return Type }

// Needs implements service.Factory.
func (Factory) Needs() service.Needs { return service.Needs{Router: true} }

// Create implements service.Factory.
func (Factory) Create(s service.Settings) (service.Endpoint, error) {
	if s.Resolver == nil {
		return nil, fmt.Errorf("the %s endpoint needs a resolver", Type)
	}
	cfg, _ := s.Config[ConfigKey].(EndpointConfig)
	cfg.Service.Logger = s.Logger.WithFields(zap.String("component", "OFREPService"))
	cfg.Service.HTTPMiddleware = s.HTTPMiddleware
	svc, err := NewOfrepService(s.Resolver, s.Store, cfg.CORS, cfg.Service, cfg.ContextValues, cfg.HeaderToContextKeyMappings)
	if err != nil {
		return nil, fmt.Errorf("error creating OFREP service: %w", err)
	}
	return endpoint{svc}, nil
}

type endpoint struct{ svc *Service }

// Serve implements service.Endpoint. When ctx ends the server closes its connections at once, the event streams
// included.
func (e endpoint) Serve(ctx context.Context) error { return e.svc.Start(ctx) }

// NewFactory returns the factory of the OFREP endpoint, as a generated main of a distribution calls it.
func NewFactory() service.Factory { return Factory{} }
