package ofrep

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature/flagd/flagd/pkg/evaluation"
	"github.com/open-feature/flagd/flagd/pkg/service"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const (
	spanTestFlags = `{
  "flags": {
    "bool-flag":   {"state": "ENABLED", "variants": {"on": true, "off": false}, "defaultVariant": "on"},
    "string-flag": {"state": "ENABLED", "variants": {"a": "x", "b": "y"}, "defaultVariant": "a"}
  }
}`
	spanTestSource  = "test-source"
	invalidSelector = "invalid=selector"
)

// Tracers may be fetched when components are built, so the provider is installed before any test
// builds one. A global provider cannot be restored; tests reset the recorder instead.
var spanRecorder = tracetest.NewSpanRecorder()

func TestMain(m *testing.M) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder)))
	os.Exit(m.Run())
}

// renderSpanTree renders ended spans as an indented tree in start order, without timestamps or IDs.
func renderSpanTree(spans []sdktrace.ReadOnlySpan) string {
	sorted := append([]sdktrace.ReadOnlySpan(nil), spans...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartTime().Before(sorted[j].StartTime()) })

	present := map[string]bool{}
	for _, s := range sorted {
		present[s.SpanContext().SpanID().String()] = true
	}
	children := map[string][]sdktrace.ReadOnlySpan{}
	var roots []sdktrace.ReadOnlySpan
	for _, s := range sorted {
		parent := s.Parent().SpanID().String()
		if present[parent] {
			children[parent] = append(children[parent], s)
		} else {
			roots = append(roots, s)
		}
	}

	var out strings.Builder
	var walk func(s sdktrace.ReadOnlySpan, depth int)
	walk = func(s sdktrace.ReadOnlySpan, depth int) {
		out.WriteString(strings.Repeat("  ", depth))
		out.WriteString(renderSpan(s))
		out.WriteString("\n")
		for _, c := range children[s.SpanContext().SpanID().String()] {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return out.String()
}

func renderSpan(s sdktrace.ReadOnlySpan) string {
	attrs := make([]string, 0, len(s.Attributes()))
	for _, a := range s.Attributes() {
		// peer addresses, ports, sizes and the user agent differ between runs and machines
		if key := string(a.Key); strings.HasPrefix(key, "network.") || strings.HasPrefix(key, "server.") ||
			strings.HasPrefix(key, "client.") || strings.HasPrefix(key, "user_agent.") || strings.HasSuffix(key, ".body.size") {
			continue
		}
		attrs = append(attrs, fmt.Sprintf("%s=%s", a.Key, a.Value.Emit()))
	}
	sort.Strings(attrs)

	line := fmt.Sprintf("%s/%s[%s]{%s}", s.InstrumentationScope().Name, s.Name(), s.SpanKind(), strings.Join(attrs, ","))
	if st := s.Status(); st.Code == codes.Error {
		line += fmt.Sprintf(" status=Error(%q)", st.Description)
	}
	for _, e := range s.Events() {
		line += " event=" + e.Name
	}
	return line
}

// TestOfrepEvaluationSpans pins the span tree of the OFREP endpoints: the otelhttp server span of the
// handler and the evaluator's internal span beneath it.
func TestOfrepEvaluationSpans(t *testing.T) {
	log := logger.NewLogger(nil, false)
	flagStore, err := store.NewStore(log, []string{spanTestSource})
	require.NoError(t, err)
	eval := evaluation.NewEvaluator(log, flagStore)
	require.NoError(t, eval.SetState(coresync.DataSync{FlagData: spanTestFlags, Source: spanTestSource}))

	server := httptest.NewServer(NewOfrepHandler(log, eval, nil, nil, nil, "flagd", SSEConfig{}, newCompression(t)))
	t.Cleanup(server.Close)

	post := func(t *testing.T, path string, header http.Header) *http.Response {
		t.Helper()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+path, strings.NewReader("{}"))
		require.NoError(t, err)
		for k, v := range header {
			req.Header[k] = v
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp
	}

	selectorHeader := func(selector string) http.Header {
		return http.Header{service.FLAGD_SELECTOR_HEADER: {selector}}
	}

	tests := []struct {
		name       string
		path       string
		header     func(t *testing.T) http.Header
		wantStatus int
		want       string
	}{
		{name: "single found", path: "/ofrep/v1/evaluate/flags/bool-flag", wantStatus: http.StatusOK, want: "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp/POST[server]{http.request.method=POST,http.response.status_code=200,url.path=/ofrep/v1/evaluate/flags/bool-flag,url.scheme=http}\n  jsonEvaluator/resolveAnyValue[internal]{}\n"},
		{name: "single not found", path: "/ofrep/v1/evaluate/flags/missing", wantStatus: http.StatusNotFound, want: "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp/POST[server]{http.request.method=POST,http.response.status_code=404,url.path=/ofrep/v1/evaluate/flags/missing,url.scheme=http}\n  jsonEvaluator/resolveAnyValue[internal]{}\n"},
		{name: "bulk found", path: "/ofrep/v1/evaluate/flags", wantStatus: http.StatusOK, want: "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp/POST[server]{http.request.method=POST,http.response.status_code=200,url.path=/ofrep/v1/evaluate/flags,url.scheme=http}\n  jsonEvaluator/resolveAll[internal]{}\n"},
		{
			name: "bulk selecting no flags", path: "/ofrep/v1/evaluate/flags", wantStatus: http.StatusOK,
			header: func(*testing.T) http.Header { return selectorHeader("source=unknown-source") },
			want:   "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp/POST[server]{http.request.method=POST,http.response.status_code=200,url.path=/ofrep/v1/evaluate/flags,url.scheme=http}\n  jsonEvaluator/resolveAll[internal]{}\n",
		},
		{
			name: "bulk not modified", path: "/ofrep/v1/evaluate/flags", wantStatus: http.StatusNotModified,
			header: func(t *testing.T) http.Header {
				// the priming request's spans are dropped by the recorder reset that follows
				etag := post(t, "/ofrep/v1/evaluate/flags", nil).Header.Get("ETag")
				require.NotEmpty(t, etag)
				return http.Header{"If-None-Match": {etag}}
			},
			want: "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp/POST[server]{http.request.method=POST,http.response.status_code=304,url.path=/ofrep/v1/evaluate/flags,url.scheme=http}\n  jsonEvaluator/resolveAll[internal]{}\n",
		},
		{
			name: "single invalid selector", path: "/ofrep/v1/evaluate/flags/bool-flag", wantStatus: http.StatusBadRequest,
			header: func(*testing.T) http.Header { return selectorHeader(invalidSelector) },
			want:   "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp/POST[server]{http.request.method=POST,http.response.status_code=400,url.path=/ofrep/v1/evaluate/flags/bool-flag,url.scheme=http}\n",
		},
		{
			name: "bulk invalid selector", path: "/ofrep/v1/evaluate/flags", wantStatus: http.StatusBadRequest,
			header: func(*testing.T) http.Header { return selectorHeader(invalidSelector) },
			want:   "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp/POST[server]{http.request.method=POST,http.response.status_code=400,url.path=/ofrep/v1/evaluate/flags,url.scheme=http}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var header http.Header
			if tt.header != nil {
				header = tt.header(t)
			}
			spanRecorder.Reset()

			resp := post(t, tt.path, header)

			require.Equal(t, tt.wantStatus, resp.StatusCode)
			require.Equal(t, tt.want, renderSpanTree(spanRecorder.Ended()))
		})
	}
}
