package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/stretchr/testify/require"
)

// tag appends its name to the X-Seen request header and the X-Order response header, so a test sees the order
// in which requests and responses pass the chain.
type tag string

func (t tag) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Add("X-Seen", string(t))
		w.Header().Add("X-Order", string(t))
		next.ServeHTTP(w, r)
	})
}

func TestChainOrder(t *testing.T) {
	var seen []string
	h := middleware.Chain([]middleware.Middleware{tag("first"), tag("second"), tag("third")},
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r.Header.Values("X-Seen")
			w.WriteHeader(http.StatusNoContent)
		}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, []string{"first", "second", "third"}, seen, "the first middleware sees a request first")
	require.Equal(t, "first,second,third", strings.Join(rec.Header().Values("X-Order"), ","))
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestChainIdentityWhenEmpty(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	for _, mws := range [][]middleware.Middleware{nil, {}} {
		got := middleware.Chain(mws, h)
		rec := httptest.NewRecorder()
		got.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusTeapot, rec.Code)
	}
}

type named string

func (n named) Type() string { return string(n) }
func (named) Create(middleware.Settings) (middleware.Middleware, error) {
	return tag("x"), nil
}

func TestValidateDeclaration(t *testing.T) {
	require.NoError(t, middleware.ValidateDeclaration(named("api-key")))
	require.Error(t, middleware.ValidateDeclaration(named(" ")))
	require.Error(t, middleware.ValidateDeclaration(nil))
}
