//nolint:dupl
package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	evaluationV2 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v2/evaluationv2connect"
	evalV2 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v2"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	flagdService "github.com/open-feature-forking/openfeature-relay/service/transport"
	"github.com/open-feature-forking/openfeature-relay/service/transport/middleware"
	corsmw "github.com/open-feature-forking/openfeature-relay/service/transport/middleware/cors"
	h2cmw "github.com/open-feature-forking/openfeature-relay/service/transport/middleware/h2c"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	ErrorPrefix = "FlagdError:"

	// flagdV2ServicePrefix is the path prefix of the evaluation v2 service, the one service the endpoint serves itself.
	flagdV2ServicePrefix = "/flagd.evaluation.v2.Service/"
)

// dispatchHandler serves the evaluation v2 service and the routes the hosted endpoints mounted. A path that none
// of them serves gets http.NotFound, the answer of every generated handler for a path it does not know.
type dispatchHandler struct {
	v2     http.Handler
	routes []flagdService.Route
}

func (d dispatchHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	path := request.URL.Path
	if strings.HasPrefix(path, flagdV2ServicePrefix) {
		d.v2.ServeHTTP(writer, request)
		return
	}
	for _, route := range d.routes {
		if strings.HasPrefix(path, route.Prefix) {
			route.Handler.ServeHTTP(writer, request)
			return
		}
	}
	http.NotFound(writer, request)
}

// buildEvaluationMetricRoutes holds every RPC procedure path of the services the endpoint serves.
func buildEvaluationMetricRoutes(files []protoreflect.FileDescriptor, mounted []flagdService.Route) map[string]struct{} {
	routes := make(map[string]struct{})
	for _, fd := range files {
		services := fd.Services()
		for i := 0; i < services.Len(); i++ {
			svc := services.Get(i)
			methods := svc.Methods()
			for j := 0; j < methods.Len(); j++ {
				routes["/"+string(svc.FullName())+"/"+string(methods.Get(j).Name())] = struct{}{}
			}
		}
	}
	for _, route := range mounted {
		for _, procedure := range route.Procedures {
			routes[procedure] = struct{}{}
		}
	}
	return routes
}

// validateRoutes refuses a route whose prefix is not a path prefix of whole services, one that overlaps the evaluation
// v2 service or another route, and one without a handler.
func validateRoutes(routes []flagdService.Route) error {
	taken := []string{flagdV2ServicePrefix}
	for _, route := range routes {
		p := route.Prefix
		if len(p) < 2 || !strings.HasPrefix(p, "/") || !strings.HasSuffix(p, "/") {
			return fmt.Errorf("the route prefix %q must start and end with a slash", p)
		}
		if route.Handler == nil {
			return fmt.Errorf("the route %q has no handler", p)
		}
		for _, other := range taken {
			if strings.HasPrefix(p, other) || strings.HasPrefix(other, p) {
				return fmt.Errorf("the route prefix %q overlaps %q", p, other)
			}
		}
		taken = append(taken, p)
	}
	return nil
}

type ConnectService struct {
	logger                *logger.Logger
	eval                  evaluator.IResolver
	telemetry             telemetry.Provider
	eventingConfiguration IEvents
	// routes are the routes of the mounted hosted endpoints.
	routes []flagdService.Route
	// metricRoutes holds every RPC procedure path the endpoint serves.
	metricRoutes   map[string]struct{}
	handlerOptions []connect.HandlerOption

	server        *http.Server
	metricsServer *http.Server

	readinessEnabled bool
	// withoutManagement leaves the management port to the runtime.
	withoutManagement bool
	// httpMiddleware is the distribution's middleware, applied after CORS and before the instrumentation.
	httpMiddleware func(http.Handler) http.Handler
	// listen opens the endpoint's listener; nil means net.Listen.
	listen service.ListenFunc
}

// DisableManagement leaves the management port, health, readiness and metrics, to the runtime: Serve then serves
// the evaluation endpoint only.
func (s *ConnectService) DisableManagement() { s.withoutManagement = true }

// NewConnectService creates the connect service; handlerOptions are applied to each of its handlers. A nil telemetry
// provider means the noop telemetry provider.
func NewConnectService(
	logger *logger.Logger, evaluator evaluator.IResolver, store store.IStore, tp telemetry.Provider,
	handlerOptions ...connect.HandlerOption,
) *ConnectService {
	tp = telemetry.OrNoop(tp)
	cs := &ConnectService{
		logger:         logger,
		eval:           flagdService.TraceEvaluations(evaluator, tp.Tracer(flagdService.EvaluatorTracerName)),
		telemetry:      tp,
		handlerOptions: handlerOptions,
		eventingConfiguration: &eventingConfiguration{
			subs:   make(map[interface{}]*subscription),
			mu:     &sync.RWMutex{},
			store:  store,
			logger: logger,
		},
	}
	return cs
}

// Serve serves services with provided configuration options
func (s *ConnectService) Serve(ctx context.Context, svcConf service.Configuration) error {
	g, gCtx := errgroup.WithContext(ctx)
	s.readinessEnabled = true

	g.Go(func() error {
		return s.startServer(gCtx, svcConf)
	})
	if !s.withoutManagement {
		g.Go(func() error {
			return s.startMetricsServer(gCtx, svcConf)
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("errgroup closed with error: %w", err)
	}
	return nil
}

// Notify emits change event notifications for subscriptions
func (s *ConnectService) Notify(n service.Notification) {
	s.eventingConfiguration.EmitToAll(n)
}

// Creates the evaluation service listener and sets up its handlers.
func (s *ConnectService) setupServer(svcConf service.Configuration) (net.Listener, error) {
	var lis net.Listener
	var err error

	if svcConf.SocketPath != "" {
		lis, err = s.listen.Or()("unix", svcConf.SocketPath)
	} else {
		address := fmt.Sprintf(":%d", svcConf.Port)
		lis, err = s.listen.Or()("tcp", address)
	}
	if err != nil {
		return nil, fmt.Errorf("error creating listener for flag evaluation service: %w", err)
	}
	s.setupHandler(svcConf)
	return lis, nil
}

// marshalOptions is the JSON configuration of every handler: falsy fields are emitted, unknown fields are discarded.
var marshalOptions = flagdService.WithJSON(
	protojson.MarshalOptions{EmitUnpopulated: true},
	protojson.UnmarshalOptions{DiscardUnknown: true},
)

// allHandlerOptions is every option the endpoint applies to its own handlers and hands to the extensions it mounts.
func (s *ConnectService) allHandlerOptions() []connect.HandlerOption {
	return append(slices.Clone(s.handlerOptions), marshalOptions)
}

// Mount mounts the hosted endpoints, which must be flagdService.EvaluationExtensions, with the configuration of the
// endpoint. A route that is refused fails the call. It has to be called before the endpoint serves.
func (s *ConnectService) Mount(cfg service.Configuration, extensions ...service.Endpoint) error {
	for _, extension := range extensions {
		ext, ok := extension.(flagdService.EvaluationExtension)
		if !ok {
			return fmt.Errorf("the hosted endpoint %T cannot be mounted: it does not implement the flag evaluation extension (Mount)", extension)
		}
		routes, err := ext.Mount(flagdService.EvaluationHost{
			Logger:         s.logger,
			Resolver:       s.eval,
			Events:         s.eventingConfiguration,
			Telemetry:      s.telemetry,
			Configuration:  cfg,
			HandlerOptions: s.allHandlerOptions(),
		})
		if err != nil {
			return fmt.Errorf("mounting %T: %w", extension, err)
		}
		next := append(slices.Clone(s.routes), routes...)
		if err := validateRoutes(next); err != nil {
			return fmt.Errorf("mounting %T: %w", extension, err)
		}
		s.routes = next
	}
	return nil
}

// metricRoute keeps the route attribute bounded to registered RPC procedures.
func (s *ConnectService) metricRoute(request *http.Request) string {
	if _, ok := s.metricRoutes[request.URL.Path]; ok {
		return request.URL.Path
	}
	return "/"
}

// Registers the v2 RPC handlers, the mounted routes and middleware.
// nolint: funlen
func (s *ConnectService) setupHandler(svcConf service.Configuration) {
	// register handler for evaluation v2 schema (with optional value and variant)

	v2Fes := NewFlagEvaluationServiceV2(s.logger.WithFields(zap.String("component", "flagd.evaluation.v2")),
		s.eval,
		s.eventingConfiguration,
		s.telemetry,
		svcConf.ContextValues,
		svcConf.HeaderToContextKeyMappings,
		svcConf.StreamDeadline,
	)

	_, v2Handler := evaluationV2.NewServiceHandler(v2Fes, s.allHandlerOptions()...)

	s.metricRoutes = buildEvaluationMetricRoutes([]protoreflect.FileDescriptor{evalV2.File_flagd_evaluation_v2_evaluation_proto}, s.routes)

	var svcHandler http.Handler = dispatchHandler{v2: v2Handler, routes: s.routes}
	if svcConf.MaxRequestBodyBytes > 0 {
		svcHandler = http.MaxBytesHandler(svcHandler, svcConf.MaxRequestBodyBytes)
	}

	s.server = &http.Server{
		ReadHeaderTimeout: time.Second,
		// slowloris/slow-client DoS protection; safe for server streams
		ReadTimeout:    5 * time.Second,
		Handler:        svcHandler,
		MaxHeaderBytes: int(svcConf.MaxRequestHeaderBytes),
		// Cipher suites and curves are left unset; FIPS mode filters them.
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}

	// Add middlewares
	s.server.Handler = s.telemetry.HTTPServerMetrics(s.server.Handler, telemetry.HTTPMetricsOptions{
		Service:          svcConf.ServiceName,
		RouteFromRequest: s.metricRoute,
	})
	if s.httpMiddleware != nil {
		s.server.Handler = s.httpMiddleware(s.server.Handler)
	}

	corsMiddleware := corsmw.New(svcConf.CORS)
	s.AddMiddleware(corsMiddleware)

	if svcConf.CertPath == "" || svcConf.KeyPath == "" {
		h2cMiddleware := h2cmw.New()
		s.AddMiddleware(h2cMiddleware)
	}
}

func (s *ConnectService) AddMiddleware(mw middleware.IMiddleware) {
	s.server.Handler = mw.Handler(s.server.Handler)
}

func (s *ConnectService) Shutdown() {
	s.readinessEnabled = false
	s.eventingConfiguration.EmitToAll(service.Notification{
		Type: service.Shutdown,
		Data: map[string]interface{}{},
	})
}

func serveWithShutdown(ctx context.Context, server *http.Server, serveFn func() error) error {
	errChan := make(chan error, 1)
	go func() { errChan <- serveFn() }()

	select {
	case err := <-errChan:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		// use a fresh context; ctx is already cancelled
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("error shutting down server: %w", err)
		}
		// wait for server to fully stop
		<-errChan
		return nil
	}
}

func (s *ConnectService) startServer(ctx context.Context, svcConf service.Configuration) error {
	lis, err := s.setupServer(svcConf)
	if err != nil {
		return err
	}
	s.logger.Info(fmt.Sprintf("Flag IResolver listening at %s", lis.Addr()))

	if svcConf.CertPath != "" && svcConf.KeyPath != "" {
		return serveWithShutdown(ctx, s.server, func() error {
			return s.server.ServeTLS(lis, svcConf.CertPath, svcConf.KeyPath)
		})
	}
	return serveWithShutdown(ctx, s.server, func() error {
		return s.server.Serve(lis)
	})
}

// startMetricsServer serves the management port of the embedder path, which only an embedder starts through Serve. It
// opens its listener with ListenAndServe, outside Settings.Listen: the runtime serves the management port itself.
func (s *ConnectService) startMetricsServer(ctx context.Context, svcConf service.Configuration) error {
	s.logger.Info(fmt.Sprintf("metrics and probes listening at %d", svcConf.ManagementPort))

	// without a metrics handler of the telemetry provider, /metrics answers 404
	var metrics http.Handler
	if p, ok := s.telemetry.(interface{ MetricsHandler() http.Handler }); ok {
		metrics = p.MetricsHandler()
	}
	s.metricsServer = flagdService.NewManagementServer(svcConf.ManagementPort, func() bool {
		return s.readinessEnabled && svcConf.ReadinessProbe()
	}, metrics)

	return serveWithShutdown(ctx, s.metricsServer, s.metricsServer.ListenAndServe)
}
