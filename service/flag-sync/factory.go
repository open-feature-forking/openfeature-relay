package sync

import (
	"context"
	"fmt"
	"slices"

	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"go.uber.org/zap"
)

// Type is the type of the flag sync service endpoint.
const Type = "flag-sync"

// ConfigKey is the key of Settings.Config under which a caller may hand the endpoint its SvcConfigurations as a Go
// value; the plain keys of decodeConfig apply on top of it.
const ConfigKey = "config"

// decodeConfig reads Settings.Config. Its keys are:
//
//	port                                  a number
//	cert-path, key-path, socket-path, service-name   strings
//	http-enabled, disable-sync-metadata   booleans
//	cors                                  a list of strings
//	context-values                        a mapping
//	stream-deadline                       a duration, such as 5s
//	max-request-header-bytes, max-request-body-bytes   numbers
func decodeConfig(config map[string]any) (SvcConfigurations, error) {
	cfg, _ := config[ConfigKey].(SvcConfigurations)
	err := service.ConfigDecoder{
		"port": service.Set(&cfg.Port, service.Uint16), "cert-path": service.Set(&cfg.CertPath, service.String),
		"key-path": service.Set(&cfg.KeyPath, service.String), "socket-path": service.Set(&cfg.SocketPath, service.String),
		"service-name": service.Set(&cfg.ServiceName, service.String), "http-enabled": service.Set(&cfg.HTTPEnabled, service.Bool),
		"disable-sync-metadata": service.Set(&cfg.DisableSyncMetadata, service.Bool), "cors": service.Set(&cfg.CORS, service.Strings),
		"context-values":           service.Set(&cfg.ContextValues, service.Map),
		"stream-deadline":          service.Set(&cfg.StreamDeadline, service.Duration),
		"max-request-header-bytes": service.Set(&cfg.MaxRequestHeaderBytes, service.Int64),
		"max-request-body-bytes":   service.Set(&cfg.MaxRequestBodyBytes, service.Int64),
	}.Decode(config, ConfigKey)
	return cfg, err
}

// connectInstrumentation is implemented by a telemetry provider that instruments ConnectRPC handlers.
type connectInstrumentation interface {
	ConnectHandlerOptions() []connect.HandlerOption
}

// Factory creates the flag sync service endpoint. It resolves no flags: it serves the flag configurations of
// the store, and announces a change to its clients when a configuration change event arrives.
type Factory struct{}

var _ service.Factory = Factory{}

// Type implements service.Factory.
func (Factory) Type() string { return Type }

// Needs implements service.Factory: the endpoint resolves nothing.
func (Factory) Needs() service.Needs { return service.Needs{} }

// Create implements service.Factory. The configuration change events are subscribed to here, before any source
// delivers, so that none is lost.
func (Factory) Create(s service.Settings) (service.Endpoint, error) {
	cfg, err := decodeConfig(s.Config)
	if err != nil {
		return nil, fmt.Errorf("the %s endpoint: %w", Type, err)
	}
	if len(cfg.Sources) == 0 {
		cfg.Sources = slices.Clone(s.Sources)
	}
	cfg.Logger = s.Logger.WithFields(zap.String("component", "FlagSyncService"))
	cfg.Store = s.Store
	cfg.Initialized = s.Initialized
	cfg.HTTPMiddleware = s.HTTPMiddleware
	cfg.Telemetry = s.Telemetry
	if p, ok := s.Telemetry.(connectInstrumentation); ok {
		cfg.Options = append(slices.Clone(cfg.Options), p.ConnectHandlerOptions()...)
	}
	svc, err := NewSyncService(cfg)
	if err != nil {
		return nil, fmt.Errorf("error creating sync service: %w", err)
	}
	e := &endpoint{svc: svc}
	if s.Bus != nil {
		e.changes = s.Bus.Subscribe(events.SubscribeOptions{
			Kinds: []events.Kind{events.KindConfigurationChange}, Buffer: 256,
		})
	}
	return e, nil
}

type endpoint struct {
	svc     *Service
	changes *events.Subscription
}

// Serve implements service.Endpoint.
func (e *endpoint) Serve(ctx context.Context) error {
	if e.changes != nil {
		defer e.changes.Close()
		go func() {
			for ev := range e.changes.C() {
				// a change dates the flags; a source's first delivery also ends the startup wait
				e.svc.Emit(ev.(events.ConfigurationChange).Source)
			}
		}()
	}
	return e.svc.Start(ctx)
}

// NewFactory returns the factory of the flag sync endpoint, as a generated main of a distribution calls it.
func NewFactory() service.Factory { return Factory{} }
