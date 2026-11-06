package flagevallegacy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	evaluationV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v1/evaluationv1connect"
	schemaConnectV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/schema/v1/schemav1connect"
	schemaGrpcV1 "buf.build/gen/go/open-feature/flagd/grpc/go/schema/v1/schemav1grpc"
	evalV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	schemaV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/schema/v1"
	"connectrpc.com/connect"
	evaluatormock "github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/mock"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	flageval "github.com/open-feature-forking/openfeature-relay/service/flag-evaluation"
	"github.com/open-feature-forking/openfeature-relay/telemetry/otel/oteltest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"
)

// post sends a JSON request to a route of the composed endpoint and returns the status and the body.
func post(t *testing.T, url string, body string, headers map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	resBody, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res.StatusCode, string(resBody)
}

// falseFlag is an evaluator whose boolean flag resolves to false.
func falseFlag(t *testing.T) *evaluatormock.MockIEvaluator {
	eval := evaluatormock.NewMockIEvaluator(gomock.NewController(t))
	eval.EXPECT().ResolveBooleanValue(gomock.Any(), gomock.Any(), "off-flag", gomock.Any()).
		Return(false, "off", model.StaticReason, map[string]any{}, nil).AnyTimes()
	return eval
}

// TestFalsyFieldsAreEmitted pins that the hosted routes use the host's JSON options: a false value is part of the
// response over ConnectRPC JSON, as it is when the host serves the services itself.
func TestFalsyFieldsAreEmitted(t *testing.T) {
	url := serveComposed(t, falseFlag(t), nil, nil, service.Configuration{})

	for _, route := range []string{"/schema.v1.Service/ResolveBoolean", "/flagd.evaluation.v1.Service/ResolveBoolean"} {
		status, body := post(t, url+route, `{"flagKey":"off-flag"}`, nil)
		require.Equal(t, http.StatusOK, status, route)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal([]byte(body), &decoded), body)
		require.Equal(t, false, decoded["value"], "%s: value is emitted: %s", route, body)
		require.Contains(t, decoded, "reason", route)
		require.Equal(t, "off", decoded["variant"], route)
	}
}

// TestRoutesHonourTheHostsConfiguration pins that the routes use the configuration of the host: its context values and
// header mappings reach the evaluation context.
func TestRoutesHonourTheHostsConfiguration(t *testing.T) {
	eval := evaluatormock.NewMockIEvaluator(gomock.NewController(t))
	eval.EXPECT().ResolveBooleanValue(gomock.Any(), gomock.Any(), "flag", map[string]any{"env": "prod", "tier": "gold"}).
		Return(true, "on", model.StaticReason, map[string]any{}, nil).Times(1)
	eval.EXPECT().ResolveBooleanValue(gomock.Any(), gomock.Any(), "flag", map[string]any{"env": "prod"}).
		Return(true, "on", model.StaticReason, map[string]any{}, nil).Times(1)
	url := serveComposed(t, eval, nil, nil, service.Configuration{
		ContextValues:              map[string]any{"env": "prod"},
		HeaderToContextKeyMappings: map[string]string{"X-Tier": "tier"},
	})

	// evaluation.v1 maps the headers; schema.v1 never did, and still does not
	status, body := post(t, url+"/flagd.evaluation.v1.Service/ResolveBoolean", `{"flagKey":"flag"}`, map[string]string{"X-Tier": "gold"})
	require.Equal(t, http.StatusOK, status, body)
	status, body = post(t, url+"/schema.v1.Service/ResolveBoolean", `{"flagKey":"flag"}`, map[string]string{"X-Tier": "gold"})
	require.Equal(t, http.StatusOK, status, body)
}

// TestV1StreamDeadlineIsTheHosts pins that the stream deadline of the host ends an evaluation.v1 event stream.
func TestV1StreamDeadlineIsTheHosts(t *testing.T) {
	url := serveComposed(t, evaluatormock.NewMockIEvaluator(gomock.NewController(t)), mustStore(t), nil,
		service.Configuration{StreamDeadline: 300 * time.Millisecond})

	stream, err := evaluationV1.NewServiceClient(http.DefaultClient, url).
		EventStream(t.Context(), connect.NewRequest(&evalV1.EventStreamRequest{}))
	require.NoError(t, err)
	defer stream.Close()
	require.True(t, stream.Receive(), "the stream starts with provider_ready")
	require.Equal(t, string(service.ProviderReady), stream.Msg().Type)

	ended := make(chan struct{})
	go func() {
		defer close(ended)
		for stream.Receive() {
		}
	}()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived the deadline of the host")
	}
}

// TestLogFields pins the component field of the log lines of each route.
func TestLogFields(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	eval := evaluatormock.NewMockIEvaluator(gomock.NewController(t))
	eval.EXPECT().ResolveBooleanValue(gomock.Any(), gomock.Any(), "missing", gomock.Any()).
		Return(false, "", model.ErrorReason, map[string]any{}, fmt.Errorf("%s", model.FlagNotFoundErrorCode)).AnyTimes()
	url := serveComposedLogged(t, logger.NewLogger(zap.New(core), true), eval, nil, nil, service.Configuration{})

	for route, component := range map[string]string{
		"/schema.v1.Service/ResolveBoolean":           "flagservice",
		"/flagd.evaluation.v1.Service/ResolveBoolean": "flagd.evaluation.v1",
	} {
		status, _ := post(t, url+route, `{"flagKey":"missing"}`, nil)
		require.Equal(t, http.StatusNotFound, status, route)
		var found bool
		for _, entry := range logs.FilterMessageSnippet("returning error response").All() {
			if entry.ContextMap()["component"] == component {
				found = true
			}
		}
		require.True(t, found, "%s logs with component=%s: %v", route, component, logs.All())
	}
}

// TestHTTPMetricLabels verifies that the mounted procedures are bounded (route, method) labels of the HTTP server
// metrics, and a path under a mounted prefix that is no procedure is labelled "/".
func TestHTTPMetricLabels(t *testing.T) {
	reader := metric.NewManualReader()
	url := serveComposed(t, evaluatormock.NewMockIEvaluator(gomock.NewController(t)), nil,
		withoutConnectOptions{telemetrytest.ProviderFor(reader)}, service.Configuration{ServiceName: "testSvc"})

	for _, req := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, schemaConnectV1.ServiceResolveAllProcedure, http.StatusMethodNotAllowed},
		{http.MethodGet, evaluationV1.ServiceResolveBooleanProcedure, http.StatusMethodNotAllowed},
		{http.MethodGet, evaluationV1.ServiceResolveBooleanProcedure + "/extra", http.StatusNotFound},
		{http.MethodGet, "/schema.v1.Service/Unknown", http.StatusNotFound},
	} {
		request, err := http.NewRequest(req.method, url+req.path, nil)
		require.NoError(t, err)
		res, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		res.Body.Close()
		require.Equal(t, req.status, res.StatusCode, "%s %s", req.method, req.path)
	}

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &data))
	want := map[[2]string]bool{
		{schemaConnectV1.ServiceResolveAllProcedure, http.MethodGet}:  true,
		{evaluationV1.ServiceResolveBooleanProcedure, http.MethodGet}: true,
		{"/", http.MethodGet}: true,
	}
	seen := collectHTTPMetricLabelPairs(t, data, want)
	require.Len(t, seen, 3)
}

// httpServerMetrics are the OTel HTTP server metric names emitted by the recorder.
var httpServerMetrics = map[string]bool{
	"http.server.request.duration":   true,
	"http.server.response.body.size": true,
	"http.server.active_requests":    true,
}

// collectHTTPMetricLabelPairs asserts each HTTP metric has exactly the want (route, method) pairs; returns names seen.
func collectHTTPMetricLabelPairs(t *testing.T, data metricdata.ResourceMetrics, want map[[2]string]bool) map[string]bool {
	t.Helper()

	seen := make(map[string]bool)
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			if !httpServerMetrics[m.Name] {
				continue
			}
			require.Equal(t, want, metricRouteMethodPairs(t, m), m.Name)
			seen[m.Name] = true
		}
	}
	return seen
}

// metricRouteMethodPairs returns the set of (route, method) label pairs on the metric's data points.
func metricRouteMethodPairs(t *testing.T, m metricdata.Metrics) map[[2]string]bool {
	t.Helper()

	got := make(map[[2]string]bool)
	collect := func(attrs attribute.Set) {
		route, _ := attrs.Value(semconv.HTTPRouteKey)
		method, _ := attrs.Value(semconv.HTTPRequestMethodKey)
		got[[2]string{route.AsString(), method.AsString()}] = true
	}
	switch points := m.Data.(type) {
	case metricdata.Histogram[float64]:
		for _, point := range points.DataPoints {
			collect(point.Attributes)
		}
	case metricdata.Sum[int64]:
		for _, point := range points.DataPoints {
			collect(point.Attributes)
		}
	default:
		t.Fatalf("unexpected data type for %s: %T", m.Name, m.Data)
	}
	return got
}

// TestUnixConnection pins that schema.v1 is served over the socket of the host with gRPC.
func TestUnixConnection(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "flagd.sock")
	eval := evaluatormock.NewMockIEvaluator(gomock.NewController(t))
	eval.EXPECT().ResolveBooleanValue(gomock.Any(), gomock.Any(), "myBoolFlag", gomock.Any()).
		Return(true, "on", model.DefaultReason, map[string]any{}, nil).AnyTimes()

	hosted, err := Factory{}.Create(service.Settings{Config: map[string]any{}})
	require.NoError(t, err)
	host, err := flageval.Factory{}.Create(service.Settings{
		Resolver: eval, Logger: logger.NewLogger(nil, false), Extensions: []service.Endpoint{hosted},
		Config: map[string]any{"socket-path": socket},
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- host.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})

	conn, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	var res *schemaV1.ResolveBooleanResponse
	require.Eventually(t, func() bool {
		res, err = schemaGrpcV1.NewServiceClient(conn).ResolveBoolean(ctx, &schemaV1.ResolveBooleanRequest{
			FlagKey: "myBoolFlag", Context: &structpb.Struct{},
		})
		return err == nil
	}, 5*time.Second, 50*time.Millisecond, "%v", err)
	require.Equal(t, "on", res.Variant)
	require.True(t, res.Value)
	require.Equal(t, model.DefaultReason, res.Reason)
}

// TestHostedEndpointIsNotServedOnItsOwn pins the error of Serve, that Create takes no settings and the declarations.
func TestHostedEndpointIsNotServedOnItsOwn(t *testing.T) {
	e, err := Factory{}.Create(service.Settings{Config: map[string]any{}})
	require.NoError(t, err)
	err = e.Serve(t.Context())
	require.EqualError(t, err, `the endpoint "flag-evaluation-legacy" is served by "flag-evaluation", not on its own`)

	_, err = Factory{}.Create(service.Settings{Config: map[string]any{"port": 1}})
	require.ErrorContains(t, err, `unknown setting "port"`)

	require.NoError(t, service.ValidateDeclarations(Factory{}))
	require.Equal(t, "flag-evaluation", Factory{}.Host())
	require.Equal(t, service.Needs{}, Factory{}.Needs())
}
