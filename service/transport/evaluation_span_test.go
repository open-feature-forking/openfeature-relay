package service_test

import (
	"context"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/open-feature-forking/openfeature-relay/service/transport"
	"github.com/stretchr/testify/require"
)

type recordingTracer struct{ started, ended []string }

func (r *recordingTracer) Start(ctx context.Context, name string, _ ...telemetry.SpanOption) (context.Context, telemetry.Span) {
	r.started = append(r.started, name)
	return ctx, recordingSpan{name: name, tracer: r}
}

type recordingSpan struct {
	name   string
	tracer *recordingTracer
}

func (recordingSpan) SetAttributes(...telemetry.Attribute) {}
func (recordingSpan) RecordError(error)                    {}
func (recordingSpan) SetError(string)                      {}
func (s recordingSpan) End()                               { s.tracer.ended = append(s.tracer.ended, s.name) }

// stubResolver answers every evaluation with a fixed result and records the flag keys it was asked for.
type stubResolver struct {
	evaluator.IResolver
	keys []string
}

func (s *stubResolver) ResolveBooleanValue(_ context.Context, _, key string, _ map[string]any) (bool, string, string, model.Metadata, error) {
	s.keys = append(s.keys, key)
	return true, "on", model.StaticReason, nil, nil
}

func (s *stubResolver) ResolveStringValue(_ context.Context, _, key string, _ map[string]any) (string, string, string, model.Metadata, error) {
	s.keys = append(s.keys, key)
	return "s", "v", model.StaticReason, nil, nil
}

func (s *stubResolver) ResolveIntValue(_ context.Context, _, key string, _ map[string]any) (int64, string, string, model.Metadata, error) {
	s.keys = append(s.keys, key)
	return 1, "v", model.StaticReason, nil, nil
}

func (s *stubResolver) ResolveFloatValue(_ context.Context, _, key string, _ map[string]any) (float64, string, string, model.Metadata, error) {
	s.keys = append(s.keys, key)
	return 1.5, "v", model.StaticReason, nil, nil
}

func (s *stubResolver) ResolveObjectValue(_ context.Context, _, key string, _ map[string]any) (map[string]any, string, string, model.Metadata, error) {
	s.keys = append(s.keys, key)
	return map[string]any{"a": 1}, "v", model.StaticReason, nil, nil
}

func (s *stubResolver) ResolveAsAnyValue(_ context.Context, _, key string, _ map[string]any) evaluator.AnyValue {
	s.keys = append(s.keys, key)
	return evaluator.AnyValue{FlagKey: key, Value: true}
}

func (s *stubResolver) ResolveAllValues(_ context.Context, _ string, _ map[string]any) ([]evaluator.AnyValue, model.Metadata, error) {
	s.keys = append(s.keys, "*")
	return []evaluator.AnyValue{{FlagKey: "x"}}, nil, nil
}

// TestTraceEvaluationsSpanNames pins the span name of each evaluation, as the flagd endpoints have always
// recorded it, and that the wrapper passes the request and the result through.
func TestTraceEvaluationsSpanNames(t *testing.T) {
	inner := &stubResolver{}
	tracer := &recordingTracer{}
	e := service.TraceEvaluations(inner, tracer)
	ctx := context.Background()

	b, _, _, _, _ := e.ResolveBooleanValue(ctx, "r", "bool", nil)
	require.True(t, b)
	s, _, _, _, _ := e.ResolveStringValue(ctx, "r", "string", nil)
	require.Equal(t, "s", s)
	i, _, _, _, _ := e.ResolveIntValue(ctx, "r", "int", nil)
	require.Equal(t, int64(1), i)
	f, _, _, _, _ := e.ResolveFloatValue(ctx, "r", "float", nil)
	require.InDelta(t, 1.5, f, 0)
	o, _, _, _, _ := e.ResolveObjectValue(ctx, "r", "object", nil)
	require.Equal(t, map[string]any{"a": 1}, o)
	require.Equal(t, "any", e.ResolveAsAnyValue(ctx, "r", "any", nil).FlagKey)
	all, _, _ := e.ResolveAllValues(ctx, "r", nil)
	require.Len(t, all, 1)

	names := []string{
		"resolveBoolean", "resolveString", "resolveInt", "resolveFloat", "resolveObject", "resolveAnyValue", "resolveAll",
	}
	require.Equal(t, names, tracer.started)
	require.Equal(t, names, tracer.ended, "every span ends")
	require.Equal(t, []string{"bool", "string", "int", "float", "object", "any", "*"}, inner.keys)
	require.Equal(t, "jsonEvaluator", service.EvaluatorTracerName)
}
