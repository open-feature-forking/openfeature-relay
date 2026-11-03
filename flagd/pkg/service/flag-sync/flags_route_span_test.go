package sync

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature/flagd/flagd/internal/telemetrytest"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// spanRecorder is the telemetry provider the tests inject, with the spans it recorded.
var spanRecorder = telemetrytest.New()

func renderSpans(spans []sdktrace.ReadOnlySpan) string {
	var out strings.Builder
	for _, s := range spans {
		attrs := make([]string, 0, len(s.Attributes()))
		for _, a := range s.Attributes() {
			attrs = append(attrs, fmt.Sprintf("%s=%s", a.Key, a.Value.Emit()))
		}
		sort.Strings(attrs)
		fmt.Fprintf(&out, "%s/%s[%s]{%s}\n", s.InstrumentationScope().Name, s.Name(), s.SpanKind(), strings.Join(attrs, ","))
	}
	return out.String()
}

// TestFlagsRouteSpans pins the server span instrumentFlagsRoute records for the flags route: one otelhttp span per
// request, named after the method, with today's attributes. Unlike the HTTP metrics, the span carries the
// client-supplied host as server.address.
func TestFlagsRouteSpans(t *testing.T) {
	const scope = "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	tests := []struct {
		name    string
		method  string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "found", method: http.MethodGet,
			handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
			want:    scope + "/GET[server]{client.address=192.0.2.1,http.request.method=GET,http.response.status_code=200,network.peer.address=192.0.2.1,network.peer.port=1234,network.protocol.version=1.1,server.address=attacker.example,server.port=9999,url.path=/v1/flags,url.scheme=http}\n",
		},
		{
			name: "not found", method: http.MethodGet,
			handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
			want:    scope + "/GET[server]{client.address=192.0.2.1,http.request.method=GET,http.response.status_code=404,network.peer.address=192.0.2.1,network.peer.port=1234,network.protocol.version=1.1,server.address=attacker.example,server.port=9999,url.path=/v1/flags,url.scheme=http}\n",
		},
		{
			name: "method not allowed", method: http.MethodPost,
			handler: methodNotAllowed,
			want:    scope + "/POST[server]{client.address=192.0.2.1,http.request.method=POST,http.response.body.size=19,http.response.status_code=405,network.peer.address=192.0.2.1,network.peer.port=1234,network.protocol.version=1.1,server.address=attacker.example,server.port=9999,url.path=/v1/flags,url.scheme=http}\n",
		},
		{
			name: "head", method: http.MethodHead,
			handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
			want:    scope + "/HEAD[server]{client.address=192.0.2.1,http.request.method=HEAD,http.response.status_code=200,network.peer.address=192.0.2.1,network.peer.port=1234,network.protocol.version=1.1,server.address=attacker.example,server.port=9999,url.path=/v1/flags,url.scheme=http}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spanRecorder.Reset()
			cfg := SvcConfigurations{Logger: logger.NewLogger(nil, false), ServiceName: "flagd", Telemetry: spanRecorder}
			h := instrumentFlagsRoute(cfg, flagsPath, tt.handler)
			req := httptest.NewRequest(tt.method, flagsPath, nil)
			req.Host = "attacker.example:9999"
			h.ServeHTTP(httptest.NewRecorder(), req)
			require.Equal(t, tt.want, renderSpans(spanRecorder.Ended()))
		})
	}
}
