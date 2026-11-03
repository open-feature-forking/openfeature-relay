package middlewaretest_test

import (
	"net/http"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware/middlewaretest"
)

// extensionMiddlewareFactory is the Extension Author's own middleware factory.
var extensionMiddlewareFactory middleware.Factory

func ExampleRun() {
	_ = func(t *testing.T) {
		middlewaretest.Run(t, extensionMiddlewareFactory, middlewaretest.Inputs{
			Config: map[string]any{"header": "X-Api-Key"},
			// a request with a valid credential
			Passed: func() *http.Request { return nil },
			// a request without one, which the middleware answers itself
			Answered: func() *http.Request { return nil },
			Binds: &middlewaretest.Binding{
				Selector:   "flagSetId=prod",
				Attributes: map[string]any{"credential": "api-key"},
			},
			InvalidConfig: map[string]any{"header": 42},
			FaultyValue:   "header",
		})
	}
}
