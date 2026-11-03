package service

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	flagdService "github.com/open-feature/flagd/flagd/pkg/service"
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

// recordSpanError marks the span as errored and returns the error for convenient inline returns.
func recordSpanError(span telemetry.Span, err error) error {
	span.RecordError(err)
	span.SetError(err.Error())
	return err
}

// MergeContextsAndHeaders merges evaluation contexts with static context values and header-based context.
// highest priority > header-context-from-cli > static-context-from-cli > request-context > lowest priority
// Header names are matched case-insensitively according to HTTP specification.
func MergeContextsAndHeaders(
	requestContext map[string]any,
	staticContext map[string]any,
	headers http.Header,
	headerToContextKeyMappings map[string]string,
) map[string]any {
	merged := make(map[string]any)

	// request-body/client context first (lowest priority)
	for k, v := range requestContext {
		merged[k] = v
	}

	// static/config context (overrides request context)
	for k, v := range staticContext {
		merged[k] = v
	}

	// header-derived context (highest priority) we use .Get which is case-insensitive
	for headerName, contextKey := range headerToContextKeyMappings {
		if value := headers.Get(headerName); value != "" {
			merged[contextKey] = value
		}
	}

	return merged
}

// flagNotFound is the resolver for a request whose bound and client's selectors conflict: no flag matches both.
func flagNotFound[T constraints](context.Context, string, string, map[string]any) (T, string, string, map[string]any, error) {
	var zero T
	return zero, "", model.ErrorReason, map[string]any{}, errors.New(model.FlagNotFoundErrorCode)
}
