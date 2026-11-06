package flagevallegacy

import (
	"context"
	"net/http"
	"testing"

	evalV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	schemaV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/schema/v1"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	evaluatormock "github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/mock"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/open-feature-forking/openfeature-relay/telemetry/otel/oteltest"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/structpb"
)

type capturedEvaluation struct {
	err       error
	reason    string
	variant   string
	key       string
	flagSetID string
}

type capturingEvaluationMetrics struct {
	evaluations []capturedEvaluation
}

func (m *capturingEvaluationMetrics) RecordEvaluation(
	_ context.Context, err error, reason, variant, key string, metadata model.Metadata,
) {
	flagSetID, _ := metadata["flagSetId"].(string)
	m.evaluations = append(m.evaluations, capturedEvaluation{
		err:       err,
		reason:    reason,
		variant:   variant,
		key:       key,
		flagSetID: flagSetID,
	})
}

// testSpan is a span the evaluation hands to resolve; these tests read nothing from it.
func testSpan(t *testing.T) telemetry.Span {
	t.Helper()

	_, span := telemetry.Noop().Tracer("test").Start(t.Context(), "test")
	return span
}

type boolResponseCapture struct{}

func (boolResponseCapture) SetResult(bool, string, string, map[string]interface{}) error {
	return nil
}

func TestTypedSingleEvaluationRecordsFlagSetID(t *testing.T) {
	metrics := &capturingEvaluationMetrics{}
	err := resolve(
		t.Context(), testSpan(t), logger.NewLogger(nil, false),
		func(context.Context, string, string, map[string]any) (bool, string, string, map[string]interface{}, error) {
			return true, "on", model.StaticReason, model.Metadata{"flagSetId": "set-a"}, nil
		},
		http.Header{}, "flag", &structpb.Struct{}, boolResponseCapture{}, metrics, nil, nil,
	)
	require.NoError(t, err)
	require.Len(t, metrics.evaluations, 1)
	require.Equal(t, "set-a", metrics.evaluations[0].flagSetID)
}

func TestTypedBulkEvaluationRecordsEachFlagSetID(t *testing.T) {
	tests := []struct {
		name string
		call func(*evaluatormock.MockIEvaluator, telemetry.Provider) error
	}{
		{
			name: "legacy schema",
			call: func(eval *evaluatormock.MockIEvaluator, tp telemetry.Provider) error {
				service := NewOldFlagEvaluationService(
					logger.NewLogger(nil, false), eval, noEvents{}, tp, nil,
				)
				_, err := service.ResolveAll(t.Context(), connect.NewRequest(&schemaV1.ResolveAllRequest{}))
				return err
			},
		},
		{
			name: "evaluation v1",
			call: func(eval *evaluatormock.MockIEvaluator, tp telemetry.Provider) error {
				service := NewFlagEvaluationService(
					logger.NewLogger(nil, false), eval, noEvents{}, tp, nil, nil, 0,
				)
				_, err := service.ResolveAll(t.Context(), connect.NewRequest(&evalV1.ResolveAllRequest{}))
				return err
			},
		},
	}

	values := []evaluator.AnyValue{
		{
			Value: true, Variant: "on", Reason: model.StaticReason, FlagKey: "flag-a",
			Metadata: model.Metadata{"flagSetId": "set-a"},
		},
		{
			Value: "value", Variant: "default", Reason: model.StaticReason, FlagKey: "flag-b",
			Metadata: model.Metadata{"flagSetId": "set-b"},
		},
		{
			Value: true, Variant: "on", Reason: model.StaticReason, FlagKey: "without-set",
			Metadata: model.Metadata{"flagSetId": 42},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			eval := evaluatormock.NewMockIEvaluator(gomock.NewController(t))
			eval.EXPECT().ResolveAllValues(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(values, model.Metadata{}, nil)
			tel := telemetrytest.New()

			require.NoError(t, test.call(eval, tel))
			evaluations := telemetrytest.Evaluations(t, tel.Metrics)
			require.Len(t, evaluations, 3)
			require.ElementsMatch(t, []string{"set-a", "set-b", ""}, []string{
				evaluations[0].FlagSetID,
				evaluations[1].FlagSetID,
				evaluations[2].FlagSetID,
			})
		})
	}
}
