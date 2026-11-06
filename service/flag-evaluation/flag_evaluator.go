package service

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	flagdService "github.com/open-feature-forking/openfeature-relay/service/transport"
)

// evaluationRecorder records an evaluation in the evaluation metrics.
type evaluationRecorder interface {
	RecordEvaluation(ctx context.Context, err error, reason, variant, key string, metadata model.Metadata)
}

type resolverSignature[T constraints] func(context context.Context, reqID, flagKey string, ctx map[string]any) (
	T, string, string, map[string]interface{}, error)

func mergeContexts(reqCtx, configFlagsCtx map[string]any, headers http.Header, headerToContextKeyMappings map[string]string) map[string]any {
	return flagdService.MergeContextsAndHeaders(reqCtx, configFlagsCtx, headers, headerToContextKeyMappings)
}

func formatContextKeys(context map[string]any) []string {
	res := []string{}
	for k := range context {
		res = append(res, k)
	}
	return res
}

func errFormat(err error) error {
	ReadableErrorMsg := model.GetErrorMessage(err.Error())
	switch err.Error() {
	case model.FlagNotFoundErrorCode, model.FlagDisabledErrorCode:
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("%s", ReadableErrorMsg))
	case model.TypeMismatchErrorCode:
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s", ReadableErrorMsg))
	case model.ParseErrorCode:
		return connect.NewError(connect.CodeDataLoss, fmt.Errorf("%s", ReadableErrorMsg))
	case model.GeneralErrorCode:
		return connect.NewError(connect.CodeUnknown, fmt.Errorf("%s", ReadableErrorMsg))
	}

	return err
}
