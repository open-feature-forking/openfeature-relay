package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/stretchr/testify/require"
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
	err := resolveV2(
		t.Context(), testSpan(t), logger.NewLogger(nil, false),
		func(context.Context, string, string, map[string]any) (bool, string, string, map[string]interface{}, error) {
			return false, "", model.ErrorReason, model.Metadata{"flagSetId": "selected-set"}, errors.New(model.FlagNotFoundErrorCode)
		},
		http.Header{}, "missing", &structpb.Struct{}, boolResponseCapture{}, metrics, nil, nil,
	)
	require.Error(t, err)
	require.Len(t, metrics.evaluations, 1)
	require.Equal(t, "selected-set", metrics.evaluations[0].flagSetID, "a selected lookup error passes its metadata to the recorder")
}
