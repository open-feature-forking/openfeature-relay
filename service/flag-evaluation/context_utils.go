package service

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	flagdService "github.com/open-feature-forking/openfeature-relay/service/transport"
	"go.uber.org/zap"
)

// errInternal is what a client learns of a request the server could not handle because of its own configuration.
var errInternal = errors.New("internal error")

func selectorFromHeader(ctx context.Context, log *logger.Logger, header http.Header) (store.Selector, error) {
	s, err := flagdService.RequestSelector(ctx, header.Get(flagdService.FLAGD_SELECTOR_HEADER))
	if errors.Is(err, flagdService.ErrInvalidBoundSelector) {
		log.Error("invalid bound selector", zap.Error(err))
		return store.Selector{}, connect.NewError(connect.CodeInternal, errInternal)
	}
	if err != nil {
		return store.Selector{}, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return s, nil
}

// flagNotFound is the resolver for a request whose bound and client's selectors conflict: no flag matches both.
func flagNotFound[T constraints](context.Context, string, string, map[string]any) (T, string, string, map[string]any, error) {
	var zero T
	return zero, "", model.ErrorReason, map[string]any{}, errors.New(model.FlagNotFoundErrorCode)
}
