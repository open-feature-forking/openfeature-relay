// Package flagevallegacy serves the deprecated flag evaluation services, schema.v1 and flagd.evaluation.v1, as a hosted
// endpoint of the flag evaluation endpoint: it opens no listener, and the host serves its routes on its own port, with its
// TLS, CORS, limits, middleware, telemetry and shutdown.
package flagevallegacy

import (
	"context"
	"fmt"

	evaluationV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v1/evaluationv1connect"
	schemaConnectV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/schema/v1/schemav1connect"
	evalV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	schemaV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/schema/v1"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	flagdService "github.com/open-feature-forking/openfeature-relay/service/transport"
	"go.uber.org/zap"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Type is the type of the deprecated flag evaluation endpoint.
const Type = "flag-evaluation-legacy"

// Host is the type of the endpoint that serves this one.
const Host = "flag-evaluation"

// Factory creates the deprecated flag evaluation endpoint. It takes no settings of its own: it rides the host's.
type Factory struct{}

var (
	_ service.Factory = Factory{}
	_ service.Hosted  = Factory{}
)

// Type implements service.Factory.
func (Factory) Type() string { return Type }

// Host implements service.Hosted: the flag evaluation endpoint serves this one.
func (Factory) Host() string { return Host }

// Needs implements service.Factory: the host's resolver is used, so the evaluation events name the host.
func (Factory) Needs() service.Needs { return service.Needs{} }

// Create implements service.Factory.
func (Factory) Create(s service.Settings) (service.Endpoint, error) {
	if err := (service.ConfigDecoder{}).Decode(s.Config); err != nil {
		return nil, fmt.Errorf("the %s endpoint: %w", Type, err)
	}
	return &endpoint{}, nil
}

// NewFactory returns the factory of the deprecated flag evaluation endpoint, as a generated main of a distribution calls it.
func NewFactory() service.Factory { return Factory{} }

type endpoint struct{}

var _ flagdService.EvaluationExtension = (*endpoint)(nil)

// Serve implements service.Endpoint. The endpoint has no listener: the host serves it.
func (*endpoint) Serve(context.Context) error {
	return fmt.Errorf("the endpoint %q is served by %q, not on its own", Type, Host)
}

// Mount implements flagdService.EvaluationExtension: schema.v1 and flagd.evaluation.v1, with the host's logger,
// resolver, subscriptions, telemetry, configuration and handler options.
func (*endpoint) Mount(h flagdService.EvaluationHost) ([]flagdService.Route, error) {
	cfg := h.Configuration

	// schema.v1 can be removed as a part of https://github.com/open-feature/flagd/issues/1088
	old := NewOldFlagEvaluationService(
		h.Logger.WithFields(zap.String("component", "flagservice")),
		h.Resolver,
		h.Events,
		h.Telemetry,
		cfg.ContextValues,
	)
	oldPath, oldHandler := schemaConnectV1.NewServiceHandler(old, h.HandlerOptions...)

	v1 := NewFlagEvaluationService(
		h.Logger.WithFields(zap.String("component", "flagd.evaluation.v1")),
		h.Resolver,
		h.Events,
		h.Telemetry,
		cfg.ContextValues,
		cfg.HeaderToContextKeyMappings,
		cfg.StreamDeadline,
	)
	v1Path, v1Handler := evaluationV1.NewServiceHandler(v1, h.HandlerOptions...)

	return []flagdService.Route{
		{Prefix: oldPath, Handler: oldHandler, Procedures: procedures(schemaV1.File_schema_v1_schema_proto)}, //nolint:staticcheck // frozen with schema.v1
		{Prefix: v1Path, Handler: v1Handler, Procedures: procedures(evalV1.File_flagd_evaluation_v1_evaluation_proto)},
	}, nil
}

// procedures lists the RPC procedure paths of the services of a proto file.
func procedures(fd protoreflect.FileDescriptor) []string {
	var paths []string
	services := fd.Services()
	for i := 0; i < services.Len(); i++ {
		svc := services.Get(i)
		methods := svc.Methods()
		for j := 0; j < methods.Len(); j++ {
			paths = append(paths, "/"+string(svc.FullName())+"/"+string(methods.Get(j).Name()))
		}
	}
	return paths
}
