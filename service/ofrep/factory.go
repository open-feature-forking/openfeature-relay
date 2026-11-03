package ofrep

import (
	"context"
	"fmt"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"go.uber.org/zap"
)

// Type is the type of the OFREP service endpoint.
const Type = "ofrep"

// ConfigKey is the key of Settings.Config under which a caller may hand the endpoint its EndpointConfig as a Go value;
// the plain keys of decodeConfig apply on top of it.
const ConfigKey = "config"

// EndpointConfig is the settings of the endpoint as a Go value.
type EndpointConfig struct {
	Service SvcConfiguration
	// Formats are the configuration formats whose documents the event stream tracks.
	Formats                    []string
	CORS                       []string
	ContextValues              map[string]any
	HeaderToContextKeyMappings map[string]string
}

// decodeConfig reads Settings.Config. Its keys are:
//
//	port                                  a number
//	service-name, sse-public-url          strings
//	sse-enabled                           a boolean
//	sse-inactivity-delay                  a number of seconds
//	cors                                  a list of strings
//	context-values                        a mapping
//	header-to-context-key-mappings        a mapping of strings
//	max-request-header-bytes, max-request-body-bytes   numbers
func decodeConfig(config map[string]any) (EndpointConfig, error) {
	cfg, _ := config[ConfigKey].(EndpointConfig)
	svc := &cfg.Service
	sseDelay := int64(svc.SSEInactivityDelaySec)
	err := service.ConfigDecoder{
		"port": service.Set(&svc.Port, service.Uint16), "service-name": service.Set(&svc.ServiceName, service.String),
		"sse-public-url": service.Set(&svc.SSEPublicURL, service.String), "sse-enabled": service.Set(&svc.SSEEnabled, service.Bool),
		"sse-inactivity-delay": service.Set(&sseDelay, service.Int64), "cors": service.Set(&cfg.CORS, service.Strings),
		"context-values":                 service.Set(&cfg.ContextValues, service.Map),
		"header-to-context-key-mappings": service.Set(&cfg.HeaderToContextKeyMappings, service.StringMap),
		"max-request-header-bytes":       service.Set(&svc.MaxRequestHeaderBytes, service.Int64),
		"max-request-body-bytes":         service.Set(&svc.MaxRequestBodyBytes, service.Int64),
	}.Decode(config, ConfigKey)
	svc.SSEInactivityDelaySec = int(sseDelay)
	return cfg, err
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
	cfg, err := decodeConfig(s.Config)
	if err != nil {
		return nil, fmt.Errorf("the %s endpoint: %w", Type, err)
	}
	if len(cfg.Formats) == 0 {
		cfg.Formats = s.Formats
	}
	cfg.Service.Logger = s.Logger.WithFields(zap.String("component", "OFREPService"))
	cfg.Service.HTTPMiddleware = s.HTTPMiddleware
	cfg.Service.Telemetry = s.Telemetry
	cfg.Service.Bus = s.Bus
	cfg.Service.Formats = cfg.Formats
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
