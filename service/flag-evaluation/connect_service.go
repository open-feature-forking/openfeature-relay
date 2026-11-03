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

	evaluationV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v1/evaluationv1connect"
	evaluationV2 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v2/evaluationv2connect"
	schemaConnectV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/schema/v1/schemav1connect"
	evalV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	evalV2 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v2"
	schemaV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/schema/v1"
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

	flagdSchemaPrefix   = "/flagd"
	flagdV2SchemaPrefix = "/flagd.evaluation.v2"
)

// bufSwitchHandler combines the handlers of the old and new evaluation schemas
// this way we support both the new (v2) and the old (v1 and deprecated) schemas
// NOTE: this will not be required anymore when it is time to work on https://github.com/open-feature/flagd/issues/1088
type bufSwitchHandler struct {
	old http.Handler
	v1  http.Handler
	v2  http.Handler
}

func (b bufSwitchHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if strings.HasPrefix(request.URL.Path, flagdV2SchemaPrefix) {
		b.v2.ServeHTTP(writer, request)
	} else if strings.HasPrefix(request.URL.Path, flagdSchemaPrefix) {
		b.v1.ServeHTTP(writer, request)
	} else {
		b.old.ServeHTTP(writer, request)
	}
}

// evaluationMetricRoutes holds every RPC procedure path of the served evaluation schemas.
var evaluationMetricRoutes = buildEvaluationMetricRoutes(
	schemaV1.File_schema_v1_schema_proto,
	evalV1.File_flagd_evaluation_v1_evaluation_proto,
	evalV2.File_flagd_evaluation_v2_evaluation_proto,
)

func buildEvaluationMetricRoutes(files ...protoreflect.FileDescriptor) map[string]struct{} {
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
	return routes
}

// evaluationMetricRoute keeps the route attribute bounded to registered RPC procedures.
func evaluationMetricRoute(request *http.Request) string {
	if _, ok := evaluationMetricRoutes[request.URL.Path]; ok {
		return request.URL.Path
	}
	return "/"
}

type ConnectService struct {
	logger                *logger.Logger
	eval                  evaluator.IResolver
	telemetry             telemetry.Provider
	eventingConfiguration IEvents
	handlerOptions        []connect.HandlerOption

	server        *http.Server
	metricsServer *http.Server

	readinessEnabled bool
	// withoutManagement leaves the management port to the runtime.
	withoutManagement bool
	// httpMiddleware is the distribution's middleware, applied after CORS and before the instrumentation.
	httpMiddleware func(http.Handler) http.Handler
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
		lis, err = net.Listen("unix", svcConf.SocketPath)
	} else {
		address := fmt.Sprintf(":%d", svcConf.Port)
		lis, err = net.Listen("tcp", address)
	}
	if err != nil {
		return nil, fmt.Errorf("error creating listener for flag evaluation service: %w", err)
	}
	s.setupHandler(svcConf)
	return lis, nil
}

// Registers the legacy, v1, and v2 RPC handlers and middleware.
// nolint: funlen
func (s *ConnectService) setupHandler(svcConf service.Configuration) {
	// register handler for old flag evaluation schema
	// can be removed as a part of https://github.com/open-feature/flagd/issues/1088
	fes := NewOldFlagEvaluationService(
		s.logger.WithFields(zap.String("component", "flagservice")),
		s.eval,
		s.eventingConfiguration,
		s.telemetry,
		svcConf.ContextValues,
	)

	marshalOpts := flagdService.WithJSON(
		// json parsing configuration - we emit "unpopulated" fields (falsy fields are not dropped)
		protojson.MarshalOptions{EmitUnpopulated: true},
		protojson.UnmarshalOptions{DiscardUnknown: true},
	)

	_, oldHandler := schemaConnectV1.NewServiceHandler(fes, append(slices.Clone(s.handlerOptions), marshalOpts)...)

	// register handler for new flag evaluation schema (v1)

	v1Fes := NewFlagEvaluationService(s.logger.WithFields(zap.String("component", "flagd.evaluation.v1")),
		s.eval,
		s.eventingConfiguration,
		s.telemetry,
		svcConf.ContextValues,
		svcConf.HeaderToContextKeyMappings,
		svcConf.StreamDeadline,
	)

	_, v1Handler := evaluationV1.NewServiceHandler(v1Fes, append(slices.Clone(s.handlerOptions), marshalOpts)...)

	// register handler for evaluation v2 schema (with optional value and variant)

	v2Fes := NewFlagEvaluationServiceV2(s.logger.WithFields(zap.String("component", "flagd.evaluation.v2")),
		s.eval,
		s.eventingConfiguration,
		s.telemetry,
		svcConf.ContextValues,
		svcConf.HeaderToContextKeyMappings,
		svcConf.StreamDeadline,
	)

	_, v2Handler := evaluationV2.NewServiceHandler(v2Fes, append(slices.Clone(s.handlerOptions), marshalOpts)...)

	bs := bufSwitchHandler{
		old: oldHandler,
		v1:  v1Handler,
		v2:  v2Handler,
	}

	var svcHandler http.Handler = bs
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
		RouteFromRequest: evaluationMetricRoute,
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
