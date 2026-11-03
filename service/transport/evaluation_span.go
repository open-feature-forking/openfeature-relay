package service

import (
	"context"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const evaluatorTracerName = "jsonEvaluator"

type tracedEvaluator struct {
	evaluator.IResolver
	tracer trace.Tracer
}

// TraceEvaluations wraps an evaluator so that every evaluation request is recorded as an internal span
// of the "jsonEvaluator" instrumentation scope.
func TraceEvaluations(e evaluator.IResolver) evaluator.IResolver {
	return &tracedEvaluator{IResolver: e, tracer: otel.Tracer(evaluatorTracerName)}
}

func (t *tracedEvaluator) ResolveBooleanValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any,
) (bool, string, string, model.Metadata, error) {
	_, span := t.tracer.Start(ctx, "resolveBoolean")
	defer span.End()
	return t.IResolver.ResolveBooleanValue(ctx, reqID, flagKey, context)
}

func (t *tracedEvaluator) ResolveStringValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any,
) (string, string, string, model.Metadata, error) {
	_, span := t.tracer.Start(ctx, "resolveString")
	defer span.End()
	return t.IResolver.ResolveStringValue(ctx, reqID, flagKey, context)
}

func (t *tracedEvaluator) ResolveIntValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any,
) (int64, string, string, model.Metadata, error) {
	_, span := t.tracer.Start(ctx, "resolveInt")
	defer span.End()
	return t.IResolver.ResolveIntValue(ctx, reqID, flagKey, context)
}

func (t *tracedEvaluator) ResolveFloatValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any,
) (float64, string, string, model.Metadata, error) {
	_, span := t.tracer.Start(ctx, "resolveFloat")
	defer span.End()
	return t.IResolver.ResolveFloatValue(ctx, reqID, flagKey, context)
}

func (t *tracedEvaluator) ResolveObjectValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any,
) (map[string]any, string, string, model.Metadata, error) {
	_, span := t.tracer.Start(ctx, "resolveObject")
	defer span.End()
	return t.IResolver.ResolveObjectValue(ctx, reqID, flagKey, context)
}

func (t *tracedEvaluator) ResolveAsAnyValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any,
) evaluator.AnyValue {
	_, span := t.tracer.Start(ctx, "resolveAnyValue")
	defer span.End()
	return t.IResolver.ResolveAsAnyValue(ctx, reqID, flagKey, context)
}

func (t *tracedEvaluator) ResolveAllValues(
	ctx context.Context, reqID string, context map[string]any,
) ([]evaluator.AnyValue, model.Metadata, error) {
	_, span := t.tracer.Start(ctx, "resolveAll")
	defer span.End()
	return t.IResolver.ResolveAllValues(ctx, reqID, context)
}
