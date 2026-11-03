package sync

import (
	"context"
	"fmt"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"go.uber.org/zap"
)

// Type is the type of the flag sync service endpoint.
const Type = "flag-sync"

// ConfigKey is the key of Settings.Config under which the runtime hands the endpoint its SvcConfigurations.
const ConfigKey = "config"

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
	cfg, _ := s.Config[ConfigKey].(SvcConfigurations)
	if port, ok := s.Config["port"].(int); ok {
		cfg.Port = uint16(port)
	}
	cfg.Logger = s.Logger.WithFields(zap.String("component", "FlagSyncService"))
	cfg.Store = s.Store
	cfg.Initialized = s.Initialized
	cfg.HTTPMiddleware = s.HTTPMiddleware
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
