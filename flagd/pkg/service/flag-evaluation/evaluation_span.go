package service

import (
	"context"

	"github.com/open-feature/flagd/core/pkg/evaluator"
	"github.com/open-feature/flagd/core/pkg/model"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const evaluatorTracerName = "jsonEvaluator"

type tracedEvaluator struct {
	evaluator.IEvaluator
	tracer trace.Tracer
}

// TraceEvaluations wraps an evaluator so that every evaluation request is recorded as an internal span
// of the "jsonEvaluator" instrumentation scope. SetState is delegated without a span.
func TraceEvaluations(e evaluator.IEvaluator) evaluator.IEvaluator {
	return &tracedEvaluator{IEvaluator: e, tracer: otel.Tracer(evaluatorTracerName)}
}

func (t *tracedEvaluator) ResolveBooleanValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any,
) (bool, string, string, model.Metadata, error) {
	_, span := t.tracer.Start(ctx, "resolveBoolean")
	defer span.End()
	return t.IEvaluator.ResolveBooleanValue(ctx, reqID, flagKey, context)
}

func (t *tracedEvaluator) ResolveStringValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any,
) (string, string, string, model.Metadata, error) {
	_, span := t.tracer.Start(ctx, "resolveString")
	defer span.End()
	return t.IEvaluator.ResolveStringValue(ctx, reqID, flagKey, context)
}

func (t *tracedEvaluator) ResolveIntValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any,
) (int64, string, string, model.Metadata, error) {
	_, span := t.tracer.Start(ctx, "resolveInt")
	defer span.End()
	return t.IEvaluator.ResolveIntValue(ctx, reqID, flagKey, context)
}

func (t *tracedEvaluator) ResolveFloatValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any,
) (float64, string, string, model.Metadata, error) {
	_, span := t.tracer.Start(ctx, "resolveFloat")
	defer span.End()
	return t.IEvaluator.ResolveFloatValue(ctx, reqID, flagKey, context)
}

func (t *tracedEvaluator) ResolveObjectValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any,
) (map[string]any, string, string, model.Metadata, error) {
	_, span := t.tracer.Start(ctx, "resolveObject")
	defer span.End()
	return t.IEvaluator.ResolveObjectValue(ctx, reqID, flagKey, context)
}

func (t *tracedEvaluator) ResolveAsAnyValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any,
) evaluator.AnyValue {
	_, span := t.tracer.Start(ctx, "resolveAnyValue")
	defer span.End()
	return t.IEvaluator.ResolveAsAnyValue(ctx, reqID, flagKey, context)
}

func (t *tracedEvaluator) ResolveAllValues(
	ctx context.Context, reqID string, context map[string]any,
) ([]evaluator.AnyValue, model.Metadata, error) {
	_, span := t.tracer.Start(ctx, "resolveAll")
	defer span.End()
	return t.IEvaluator.ResolveAllValues(ctx, reqID, context)
}
