package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	evaluationV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v1/evaluationv1connect"
	schemaConnectV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/schema/v1/schemav1connect"
	schemaGrpcV1 "buf.build/gen/go/open-feature/flagd/grpc/go/schema/v1/schemav1grpc"
	evalV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	schemaV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/schema/v1"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	flagdService "github.com/open-feature-forking/openfeature-relay/service/transport"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func serveHostAlone(t *testing.T) *httptest.Server {
	t.Helper()
	svc := NewConnectService(logger.NewLogger(nil, false), nil, nil, nil)
	svc.setupHandler(service.Configuration{ServiceName: "dispatch"})
	server := httptest.NewServer(svc.server.Handler)
	t.Cleanup(server.Close)
	return server
}

// TestDeprecatedServicesAreUnimplementedWithoutAHostedEndpoint pins what a distribution without the legacy endpoint
// answers on the deprecated services.
func TestDeprecatedServicesAreUnimplementedWithoutAHostedEndpoint(t *testing.T) {
	server := serveHostAlone(t)

	_, err := schemaConnectV1.NewServiceClient(http.DefaultClient, server.URL).
		ResolveBoolean(t.Context(), connect.NewRequest(&schemaV1.ResolveBooleanRequest{FlagKey: "f"})) //nolint:staticcheck // asserts the deprecated service is absent
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
	_, err = evaluationV1.NewServiceClient(http.DefaultClient, server.URL).
		ResolveBoolean(t.Context(), connect.NewRequest(&evalV1.ResolveBooleanRequest{FlagKey: "f"}))
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))

	conn, err := grpc.NewClient(server.Listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	_, err = schemaGrpcV1.NewServiceClient(conn).ResolveBoolean(context.Background(), &schemaV1.ResolveBooleanRequest{FlagKey: "f"}) //nolint:staticcheck // see above
	require.Equal(t, codes.Unimplemented, status.Code(err))

	for _, path := range []string{"/schema.v1.Service/ResolveBoolean", "/flagd.evaluation.v1.Service/ResolveBoolean"} {
		res, err := http.Post(server.URL+path, "application/json", strings.NewReader(`{}`))
		require.NoError(t, err)
		res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode, path)
	}
}

// TestUnknownPathsAnswerNotFound pins the answer for a path no handler serves: the status, body and headers of
// http.NotFound, which every generated handler writes for a path it does not know.
func TestUnknownPathsAnswerNotFound(t *testing.T) {
	server := serveHostAlone(t)

	for _, path := range []string{
		"/", "/unknown", "/flagdX", "/flagd.evaluation.v2.Service", "/flagd.evaluation.v2",
		"/flagd.evaluation.v1.Service", "/schema.v1.Service", "/flagd.evaluation.v2.Service/Unknown",
	} {
		res, err := http.Post(server.URL+path, "application/json", strings.NewReader(`{}`))
		require.NoError(t, err)
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, res.StatusCode, path)
		require.Equal(t, "404 page not found\n", string(body), path)
		require.Equal(t, "text/plain; charset=utf-8", res.Header.Get("Content-Type"), path)
		require.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"), path)
	}
}

type stubExtension struct {
	routes []flagdService.Route
	err    error
	host   *flagdService.EvaluationHost
}

func (s *stubExtension) Serve(context.Context) error { return nil }

func (s *stubExtension) Mount(h flagdService.EvaluationHost) ([]flagdService.Route, error) {
	s.host = &h
	return s.routes, s.err
}

func okHandler(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
}

func TestMountedRoutesAreServedAndValidated(t *testing.T) {
	require.True(t, Factory{}.Hosts())

	bad := map[string][]flagdService.Route{
		"no leading slash":          {{Prefix: "x.Service/", Handler: okHandler(200)}},
		"no trailing slash":         {{Prefix: "/x.Service", Handler: okHandler(200)}},
		"the root":                  {{Prefix: "/", Handler: okHandler(200)}},
		"inside v2":                 {{Prefix: "/flagd.evaluation.v2.Service/Resolve/", Handler: okHandler(200)}},
		"contains v2":               {{Prefix: "/flagd.", Handler: okHandler(200)}},
		"v2 itself":                 {{Prefix: "/flagd.evaluation.v2.Service/", Handler: okHandler(200)}},
		"no handler":                {{Prefix: "/x.Service/"}},
		"overlaps another route":    {{Prefix: "/x.Service/", Handler: okHandler(200)}, {Prefix: "/x.Service/y/", Handler: okHandler(200)}},
		"the same route twice":      {{Prefix: "/x.Service/", Handler: okHandler(200)}, {Prefix: "/x.Service/", Handler: okHandler(200)}},
		"a prefix of another route": {{Prefix: "/x.Service/y/", Handler: okHandler(200)}, {Prefix: "/x.Service/", Handler: okHandler(200)}},
	}
	for name, routes := range bad {
		t.Run(name, func(t *testing.T) {
			svc := NewConnectService(logger.NewLogger(nil, false), nil, nil, nil)
			require.Error(t, svc.Mount(service.Configuration{}, &stubExtension{routes: routes}))
		})
	}

	t.Run("a hosted endpoint that does not mount", func(t *testing.T) {
		svc := NewConnectService(logger.NewLogger(nil, false), nil, nil, nil)
		err := svc.Mount(service.Configuration{}, notMountable{})
		require.ErrorContains(t, err, "does not implement the flag evaluation extension")
	})

	t.Run("routes are served and receive the host", func(t *testing.T) {
		ext := &stubExtension{routes: []flagdService.Route{
			{Prefix: "/x.Service/", Handler: okHandler(http.StatusTeapot), Procedures: []string{"/x.Service/Do"}},
		}}
		svc := NewConnectService(logger.NewLogger(nil, false), nil, nil, nil)
		require.NoError(t, svc.Mount(service.Configuration{Port: 7}, ext))
		require.NotNil(t, ext.host)
		require.Equal(t, uint16(7), ext.host.Configuration.Port)
		require.NotNil(t, ext.host.Telemetry)
		require.NotEmpty(t, ext.host.HandlerOptions, "the JSON marshal options are among the handler options")

		svc.setupHandler(service.Configuration{})
		server := httptest.NewServer(svc.server.Handler)
		defer server.Close()
		res, err := http.Post(server.URL+"/x.Service/Do", "application/json", strings.NewReader(`{}`))
		require.NoError(t, err)
		res.Body.Close()
		require.Equal(t, http.StatusTeapot, res.StatusCode)
		res, err = http.Post(server.URL+"/x.ServiceX/Do", "application/json", strings.NewReader(`{}`))
		require.NoError(t, err)
		res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode)
	})
}

type notMountable struct{}

func (notMountable) Serve(context.Context) error { return nil }
