package service

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	flagdService "github.com/open-feature-forking/openfeature-relay/service/transport"
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
func recordSpanError(span telemetry.Span, err error) error {
	span.RecordError(err)
	span.SetError(err.Error())
	return err
}
