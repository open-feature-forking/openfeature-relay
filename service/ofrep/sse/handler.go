package sse

import (
	"context"
	"errors"
	"net/http"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/service/transport"
	"go.uber.org/zap"
)

// channelPathVar is the path wildcard carrying the selector expression the client wants change
// notifications for, using the same syntax as the Flagd-Selector header. An empty channel (the
// bare stream path) selects every flag. See Service.Register for the routes it is bound to.
const channelPathVar = "channel"

// Handler resolves the request's selector, takes a reference on the matching subscription so the
// store watch stays alive, and streams events until the client disconnects.
//
// It must be mounted through Service.Register: the channel is read from the request path, so the
// route has to declare the channel wildcard.
func (svc *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// already percent-decoded by the mux, so a source containing "/" arrives intact
		channel := r.PathValue(channelPathVar)
		selector, err := service.RequestSelector(r.Context(), channel)
		if errors.Is(err, service.ErrInvalidBoundSelector) {
			if svc.logger != nil {
				svc.logger.Error("invalid bound selector", zap.Error(err))
			}
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if err != nil {
			// not echoing the expression back: it is unescaped client input
			http.Error(w, "invalid selector in the channel path segment", http.StatusBadRequest)
			return
		}

		channel = Channel(r.Context(), channel, selector)
		release, err := svc.tracker.Subscribe(channel, selector)
		if err != nil {
			http.Error(w, "unable to subscribe to flag changes", http.StatusServiceUnavailable)
			return
		}
		defer release()

		svc.es.Handler(channel).ServeHTTP(w, r)
	})
}

// Channel returns the channel of a request's stream and of the document versions behind it: the expression as the
// client sent it, or, when a middleware bound a selector to the request, one named after the effective selector,
// so that requests bound differently never share a channel.
func Channel(ctx context.Context, expression string, effective store.Selector) string {
	if _, bound, _ := middleware.BoundSelector(ctx); !bound {
		return expression
	}
	return "bound:" + effective.ToLogString()
}
