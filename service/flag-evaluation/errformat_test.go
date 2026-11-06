package service

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/stretchr/testify/require"
)

// TestErrFormat pins the mapping of an evaluation error to a connect code, which SDKs rely on to tell flag errors from
// transport errors, and the readable message each carries.
func TestErrFormat(t *testing.T) {
	tests := map[string]connect.Code{
		model.FlagNotFoundErrorCode: connect.CodeNotFound,
		model.FlagDisabledErrorCode: connect.CodeNotFound,
		model.TypeMismatchErrorCode: connect.CodeInvalidArgument,
		model.ParseErrorCode:        connect.CodeDataLoss,
		model.GeneralErrorCode:      connect.CodeUnknown,
	}
	for code, want := range tests {
		t.Run(code, func(t *testing.T) {
			var connectErr *connect.Error
			require.ErrorAs(t, errFormat(errors.New(code)), &connectErr)
			require.Equal(t, want, connectErr.Code())
			require.Equal(t, model.GetErrorMessage(code), connectErr.Message())
		})
	}

	other := errors.New("something else")
	require.Same(t, other, errFormat(other), "an error that is no evaluation code passes through")
}
