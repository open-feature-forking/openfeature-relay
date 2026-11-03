package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"go.uber.org/zap"
)

// Type is the type of the flag evaluation service endpoint.
const Type = "flag-evaluation"

// ConfigKey is the key of Settings.Config under which the runtime hands the endpoint its EndpointConfig.
const ConfigKey = "config"

// EndpointConfig is what the runtime derives from the operator's settings for the endpoint.
type EndpointConfig struct {
	Configuration service.Configuration
}

// connectInstrumentation is implemented by a telemetry provider that instruments ConnectRPC handlers.
type connectInstrumentation interface {
	ConnectHandlerOptions() []connect.HandlerOption
}

// Factory creates the flag evaluation service endpoint: the legacy, v1 and v2 flag evaluation services over
// connect, gRPC and HTTP. It resolves through the evaluation router and publishes nothing by hand.
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
	if port, ok := s.Config["port"].(int); ok {
		cfg.Configuration.Port = uint16(port)
	}
	var options []connect.HandlerOption
	if p, ok := s.Telemetry.(connectInstrumentation); ok {
		options = p.ConnectHandlerOptions()
	}
	svc := NewConnectService(s.Logger.WithFields(zap.String("component", "service")), s.Resolver, s.Store, s.Telemetry, options...)
	svc.httpMiddleware = s.HTTPMiddleware
	return &endpoint{svc: svc, cfg: cfg.Configuration}, nil
}

type endpoint struct {
	svc *ConnectService
	cfg service.Configuration
}

// Serve implements service.Endpoint. When ctx ends it sends provider_shutdown to every open event stream, ends
// the streams and shuts the server down, so it returns well within the endpoint budget.
func (e *endpoint) Serve(ctx context.Context) error {
	return e.svc.serveEndpoint(ctx, e.cfg)
}

const shutdownBudget = 5 * time.Second

func (s *ConnectService) serveEndpoint(ctx context.Context, svcConf service.Configuration) error {
	lis, err := s.setupServer(svcConf)
	if err != nil {
		return err
	}
	s.logger.Info(fmt.Sprintf("Flag IResolver listening at %s", lis.Addr()))

	// the requests of the server end with this context, which is cancelled once the streams' last notification is queued.
	// A unary request still running at that moment is cancelled too, where the graceful Shutdown would have drained it;
	// evaluations do not wait on their context, so they finish and answer.
	streams, endStreams := context.WithCancel(context.Background())
	defer endStreams()
	s.server.BaseContext = func(net.Listener) context.Context { return streams }

	errs := make(chan error, 1)
	go func() {
		if svcConf.CertPath != "" && svcConf.KeyPath != "" {
			errs <- s.server.ServeTLS(lis, svcConf.CertPath, svcConf.KeyPath)
			return
		}
		errs <- s.server.Serve(lis)
	}()

	select {
	case err := <-errs:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		s.Notify(service.Notification{Type: service.Shutdown, Data: map[string]interface{}{}})
		endStreams()
		shutdown, cancel := context.WithTimeout(context.Background(), shutdownBudget)
		defer cancel()
		if err := s.server.Shutdown(shutdown); err != nil {
			return fmt.Errorf("error shutting down server: %w", err)
		}
		<-errs
		return nil
	}
}

// NewFactory returns the factory of the flag evaluation endpoint, as a generated main of a distribution calls it.
func NewFactory() service.Factory { return Factory{} }
