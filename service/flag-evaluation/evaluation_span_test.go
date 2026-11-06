package service

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	evaluationV2 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v2/evaluationv2connect"
	evalV2 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v2"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	iservice "github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	evaluation "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	flagdService "github.com/open-feature-forking/openfeature-relay/service/transport"
	"github.com/open-feature-forking/openfeature-relay/telemetry/otel/oteltest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const spanTestFlags = `{
  "flags": {
    "bool-flag":   {"state": "ENABLED", "variants": {"on": true, "off": false}, "defaultVariant": "on"},
    "string-flag": {"state": "ENABLED", "variants": {"a": "x", "b": "y"}, "defaultVariant": "a"},
    "int-flag":    {"state": "ENABLED", "variants": {"one": 1, "two": 2}, "defaultVariant": "one"},
    "float-flag":  {"state": "ENABLED", "variants": {"a": 1.5, "b": 2.5}, "defaultVariant": "a"},
    "object-flag": {"state": "ENABLED", "variants": {"a": {"k": "v"}}, "defaultVariant": "a"}
  }
}`

const (
	spanTestSource  = "test-source"
	invalidSelector = "invalid=selector"
)

// spanRecorder is the telemetry provider the tests inject, with the spans it recorded.
var spanRecorder = telemetrytest.New()

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

type evaluationSpanClients struct {
	v2 evaluationV2.ServiceClient
}

func newEvaluationSpanClients(t *testing.T) evaluationSpanClients {
	t.Helper()

	log := logger.NewLogger(nil, false)
	flagStore, err := store.NewStore(log, []string{spanTestSource})
	require.NoError(t, err)
	eval := evaluation.NewEvaluator(log, flagStore)
	require.NoError(t, eval.SetState(coresync.DataSync{FlagData: spanTestFlags, Source: spanTestSource}))

	svc := NewConnectService(log, eval, flagStore, spanRecorder)
	svc.setupHandler(iservice.Configuration{ServiceName: "spanTest"})
	server := httptest.NewServer(svc.server.Handler)
	t.Cleanup(server.Close)

	return evaluationSpanClients{
		v2: evaluationV2.NewServiceClient(server.Client(), server.URL),
	}
}

func withSelector[T any](msg *T, selector string) *connect.Request[T] {
	req := connect.NewRequest(msg)
	if selector != "" {
		req.Header().Set(flagdService.FLAGD_SELECTOR_HEADER, selector)
	}
	return req
}

type evaluationCall func(ctx context.Context, c evaluationSpanClients, key, selector string) error

var evaluationCalls = map[string]evaluationCall{
	"v2/Boolean": func(ctx context.Context, c evaluationSpanClients, key, sel string) error {
		_, err := c.v2.ResolveBoolean(ctx, withSelector(&evalV2.ResolveBooleanRequest{FlagKey: key}, sel))
		return err
	},
	"v2/String": func(ctx context.Context, c evaluationSpanClients, key, sel string) error {
		_, err := c.v2.ResolveString(ctx, withSelector(&evalV2.ResolveStringRequest{FlagKey: key}, sel))
		return err
	},
	"v2/Int": func(ctx context.Context, c evaluationSpanClients, key, sel string) error {
		_, err := c.v2.ResolveInt(ctx, withSelector(&evalV2.ResolveIntRequest{FlagKey: key}, sel))
		return err
	},
	"v2/Float": func(ctx context.Context, c evaluationSpanClients, key, sel string) error {
		_, err := c.v2.ResolveFloat(ctx, withSelector(&evalV2.ResolveFloatRequest{FlagKey: key}, sel))
		return err
	},
	"v2/Object": func(ctx context.Context, c evaluationSpanClients, key, sel string) error {
		_, err := c.v2.ResolveObject(ctx, withSelector(&evalV2.ResolveObjectRequest{FlagKey: key}, sel))
		return err
	},
}

// TestEvaluationSpans pins the span tree of every flag evaluation RPC: the service's server span and
// the evaluator's internal span beneath it.
func TestEvaluationSpans(t *testing.T) {
	clients := newEvaluationSpanClients(t)

	tests := []struct {
		call     string
		key      string
		selector string
		wantErr  bool
		want     string
	}{
		{call: "v2/Boolean", key: "bool-flag", selector: "", wantErr: false, want: "flagd.evaluation.v2/resolveBoolean[server]{feature_flag.key=bool-flag,feature_flag.provider.name=flagd,feature_flag.result.variant=on}\n  jsonEvaluator/resolveBoolean[internal]{}\n"},
		{call: "v2/Boolean", key: "missing", selector: "", wantErr: true, want: "flagd.evaluation.v2/resolveBoolean[server]{feature_flag.key=missing,feature_flag.provider.name=flagd,feature_flag.result.variant=} status=Error(\"error evaluating flag with key missing\") event=exception\n  jsonEvaluator/resolveBoolean[internal]{}\n"},
		{call: "v2/String", key: "string-flag", selector: "", wantErr: false, want: "flagd.evaluation.v2/resolveString[server]{feature_flag.key=string-flag,feature_flag.provider.name=flagd,feature_flag.result.variant=a}\n  jsonEvaluator/resolveString[internal]{}\n"},
		{call: "v2/String", key: "missing", selector: "", wantErr: true, want: "flagd.evaluation.v2/resolveString[server]{feature_flag.key=missing,feature_flag.provider.name=flagd,feature_flag.result.variant=} status=Error(\"error evaluating flag with key missing\") event=exception\n  jsonEvaluator/resolveString[internal]{}\n"},
		{call: "v2/Int", key: "int-flag", selector: "", wantErr: false, want: "flagd.evaluation.v2/resolveInt[server]{feature_flag.key=int-flag,feature_flag.provider.name=flagd,feature_flag.result.variant=one}\n  jsonEvaluator/resolveInt[internal]{}\n"},
		{call: "v2/Int", key: "missing", selector: "", wantErr: true, want: "flagd.evaluation.v2/resolveInt[server]{feature_flag.key=missing,feature_flag.provider.name=flagd,feature_flag.result.variant=} status=Error(\"error evaluating flag with key missing\") event=exception\n  jsonEvaluator/resolveInt[internal]{}\n"},
		{call: "v2/Float", key: "float-flag", selector: "", wantErr: false, want: "flagd.evaluation.v2/resolveFloat[server]{feature_flag.key=float-flag,feature_flag.provider.name=flagd,feature_flag.result.variant=a}\n  jsonEvaluator/resolveFloat[internal]{}\n"},
		{call: "v2/Float", key: "missing", selector: "", wantErr: true, want: "flagd.evaluation.v2/resolveFloat[server]{feature_flag.key=missing,feature_flag.provider.name=flagd,feature_flag.result.variant=} status=Error(\"error evaluating flag with key missing\") event=exception\n  jsonEvaluator/resolveFloat[internal]{}\n"},
		{call: "v2/Object", key: "object-flag", selector: "", wantErr: false, want: "flagd.evaluation.v2/resolveObject[server]{feature_flag.key=object-flag,feature_flag.provider.name=flagd,feature_flag.result.variant=a}\n  jsonEvaluator/resolveObject[internal]{}\n"},
		{call: "v2/Object", key: "missing", selector: "", wantErr: true, want: "flagd.evaluation.v2/resolveObject[server]{feature_flag.key=missing,feature_flag.provider.name=flagd,feature_flag.result.variant=} status=Error(\"error evaluating flag with key missing\") event=exception\n  jsonEvaluator/resolveObject[internal]{}\n"},
		{call: "v2/Boolean", key: "bool-flag", selector: invalidSelector, wantErr: true, want: "flagd.evaluation.v2/resolveBoolean[server]{} status=Error(\"invalid_argument: invalid selector key \\\"invalid\\\", valid keys: \\\"flagSetId\\\", \\\"source\\\"\") event=exception\n"},
	}

	for _, tt := range tests {
		t.Run(subtestName(tt.call, tt.key, tt.selector), func(t *testing.T) {
			spanRecorder.Reset()

			err := evaluationCalls[tt.call](t.Context(), clients, tt.key, tt.selector)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.want, renderSpanTree(spanRecorder.Ended()))
		})
	}
}

// subtestName joins the non-empty parts with underscores; a slash would nest the subtest.
func subtestName(parts ...string) string {
	var named []string
	for _, p := range parts {
		if p != "" {
			named = append(named, strings.ReplaceAll(p, "/", "_"))
		}
	}
	return strings.Join(named, "_")
}
