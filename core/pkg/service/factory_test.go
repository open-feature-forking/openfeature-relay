package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeFactory struct {
	typeName string
	needs    Needs
}

func (f fakeFactory) Type() string                    { return f.typeName }
func (f fakeFactory) Needs() Needs                    { return f.needs }
func (fakeFactory) Create(Settings) (Endpoint, error) { return fakeEndpoint{}, nil }

type fakeEndpoint struct{}

func (fakeEndpoint) Serve(context.Context) error { return nil }

func TestValidateDeclarations(t *testing.T) {
	tests := []struct {
		name    string
		factory fakeFactory
		wantErr string
	}{
		{name: "an endpoint that resolves nothing", factory: fakeFactory{typeName: "flag-sync"}},
		{name: "through the router", factory: fakeFactory{typeName: "rest", needs: Needs{Router: true}}},
		{name: "a vendor's evaluator", factory: fakeFactory{typeName: "vendor", needs: Needs{Format: "keyvalue"}}},
		{name: "the router and a vendor's evaluator", factory: fakeFactory{typeName: "vendor", needs: Needs{Router: true, Format: "keyvalue"}}},
		{name: "no type name", factory: fakeFactory{}, wantErr: "the type name is empty"},
		{name: "a blank type name", factory: fakeFactory{typeName: "  "}, wantErr: "the type name is empty"},
		{
			name:    "a format with whitespace",
			factory: fakeFactory{typeName: "vendor", needs: Needs{Format: " keyvalue"}},
			wantErr: "the needed configuration format has surrounding whitespace",
		},
		{
			name:    "every broken rule is named",
			factory: fakeFactory{needs: Needs{Format: "keyvalue "}},
			wantErr: "the type name is empty; the needed configuration format has surrounding whitespace",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDeclarations(tt.factory)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

type hostedFactory struct {
	fakeFactory
	host string
}

func (f hostedFactory) Host() string { return f.host }

type hostingFactory struct {
	fakeFactory
	hosts bool
}

func (f hostingFactory) Hosts() bool { return f.hosts }

var _ Hosting = hostingFactory{}

func TestValidateDeclarationsHost(t *testing.T) {
	tests := []struct {
		name    string
		factory Factory
		wantErr string
	}{
		{name: "a hosted endpoint", factory: hostedFactory{fakeFactory{typeName: "legacy"}, "flag-evaluation"}},
		{name: "no host", factory: hostedFactory{fakeFactory{typeName: "legacy"}, ""}, wantErr: "the host is empty"},
		{name: "a blank host", factory: hostedFactory{fakeFactory{typeName: "legacy"}, " "}, wantErr: "the host has surrounding whitespace"},
		{name: "a host with whitespace", factory: hostedFactory{fakeFactory{typeName: "legacy"}, "flag-evaluation "}, wantErr: "the host has surrounding whitespace"},
		{name: "its own type", factory: hostedFactory{fakeFactory{typeName: "legacy"}, "legacy"}, wantErr: "the host is the endpoint's own type"},
		{
			name:    "every broken rule is named",
			factory: hostedFactory{fakeFactory{needs: Needs{Format: " x"}}, ""},
			wantErr: "the type name is empty; the needed configuration format has surrounding whitespace; the host is empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDeclarations(tt.factory)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestWrapHTTP(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Inner", "yes") })

	t.Run("without middleware it is the identity", func(t *testing.T) {
		rec := httptest.NewRecorder()
		Settings{}.WrapHTTP(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, "yes", rec.Header().Get("X-Inner"))
	})

	t.Run("with middleware", func(t *testing.T) {
		settings := Settings{HTTPMiddleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Outer", "yes")
				next.ServeHTTP(w, r)
			})
		}}
		rec := httptest.NewRecorder()
		settings.WrapHTTP(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, "yes", rec.Header().Get("X-Outer"))
		require.Equal(t, "yes", rec.Header().Get("X-Inner"))
	})
}
