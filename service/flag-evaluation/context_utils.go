package service

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	flagdService "github.com/open-feature-forking/openfeature-relay/service/transport"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func selectorFromHeader(header http.Header) (store.Selector, error) {
	expr := header.Get(flagdService.FLAGD_SELECTOR_HEADER)
	s, err := store.NewSelector(expr)
	if err != nil {
		return store.Selector{}, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return s, nil
}

// recordSpanError marks the span as errored and returns the error for convenient inline returns.
func recordSpanError(span trace.Span, err error) error {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	return err
}
