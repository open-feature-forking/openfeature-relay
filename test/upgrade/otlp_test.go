package upgrade

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
)

// receiver is an OTLP trace receiver that keeps what it is sent.
type receiver struct {
	coltrace.UnimplementedTraceServiceServer
	mu    sync.Mutex
	spans []span
}

type span struct {
	scope, name, kind, status string
	attributes                []string
	id, parent                string
}

func (r *receiver) Export(_ context.Context, req *coltrace.ExportTraceServiceRequest) (*coltrace.ExportTraceServiceResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rs := range req.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				r.spans = append(r.spans, span{
					scope: ss.GetScope().GetName(), name: s.Name, kind: tracepb.Span_SpanKind_name[int32(s.Kind)],
					status:     tracepb.Status_StatusCode_name[int32(s.GetStatus().GetCode())],
					attributes: attributes(s.Attributes), id: fmt.Sprintf("%x", s.SpanId), parent: fmt.Sprintf("%x", s.ParentSpanId),
				})
			}
		}
	}
	return &coltrace.ExportTraceServiceResponse{}, nil
}

// volatile attribute keys carry something that differs between two runs of the same flagd: the address and port of the
// peer and of the published port. They match by exact name.
var volatile = map[string]bool{
	"server.address": true, "server.port": true, "client.address": true, "client.port": true,
	"network.peer.address": true, "network.peer.port": true, "network.local.address": true, "network.local.port": true,
	"net.peer.name": true, "net.peer.port": true, "net.host.name": true, "net.host.port": true,
	"net.sock.peer.addr": true, "net.sock.peer.port": true, "net.sock.host.addr": true, "net.sock.host.port": true,
	"url.full": true, "http.url": true,
}

func attributes(kvs []*commonpb.KeyValue) []string {
	var out []string
	for _, kv := range kvs {
		value := kv.GetValue().GetStringValue()
		if value == "" {
			value = fmt.Sprint(kv.GetValue())
		}
		if volatile[kv.Key] {
			value = "<varies>"
		}
		out = append(out, kv.Key+"="+value)
	}
	sort.Strings(out)
	return out
}

// lines renders the spans without ids or timing: scope, name, kind, status, attributes and the name of the parent.
func (r *receiver) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := map[string]string{}
	for _, s := range r.spans {
		names[s.id] = s.name
	}
	var out []string
	for _, s := range r.spans {
		parent := "<root>"
		if p, ok := names[s.parent]; ok {
			parent = p
		}
		out = append(out, fmt.Sprintf("%s | %s | %s | %s | parent=%s | %s", s.scope, s.name, s.kind, s.status, parent, strings.Join(s.attributes, ",")))
	}
	sort.Strings(out)
	return out
}

// spansOf starts the image with its traces exported to a receiver of this test, sends the requests of the differential
// and returns the spans the receiver saw.
func spansOf(t *testing.T, image string) []string {
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	rec := &receiver{}
	coltrace.RegisterTraceServiceServer(server, rec)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	port := listener.Addr().(*net.TCPAddr).Port

	r := startWith(t, image,
		[]string{"start", "--uri", "file:/data/flags.json", "--otel-collector-uri", fmt.Sprintf("host.docker.internal:%d", port)},
		map[string]string{"OTEL_EXPORTER_OTLP_INSECURE": "true", "OTEL_BSP_SCHEDULE_DELAY": "500"},
		map[string]string{"flags.json": flagsJSON}, []string{"--add-host=host.docker.internal:host-gateway"})
	require.True(t, r.ready(60*time.Second), "%s did not become ready: %s", image, r.logs())

	for _, req := range []struct{ port, path, body string }{
		{portEvaluation, "/flagd.evaluation.v1.Service/ResolveBoolean", `{"flagKey":"bool-flag","context":{}}`},
		{portEvaluation, "/flagd.evaluation.v1.Service/ResolveAll", `{"context":{"tier":"gold"}}`},
		{portOFREP, "/ofrep/v1/evaluate/flags/string-flag", `{"context":{"tier":"gold"}}`},
		{portOFREP, "/ofrep/v1/evaluate/flags", `{"context":{}}`},
		{portSync, "/flagd.sync.v1.FlagSyncService/FetchAllFlags", `{}`},
	} {
		status, _, _, err := r.do(http.MethodPost, req.port, req.path, req.body, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, req.path)
	}
	// the batch processor exports every half second here
	require.Eventually(t, func() bool { return len(rec.lines()) > 0 }, 20*time.Second, 200*time.Millisecond, "no span arrived from %s: %s", image, r.logs())
	time.Sleep(2 * time.Second)
	return rec.lines()
}

// TestSpans is BR-006 and FR-033 at binary level: both flagds export spans with the same names, kinds, scopes,
// attributes, status and parent links for the same requests. Ids and timing are left out.
func TestSpans(t *testing.T) {
	baseline, candidate := images(t)
	before, after := spansOf(t, baseline), spansOf(t, candidate)
	require.NotEmpty(t, before, "the baseline exported no span")
	require.Equal(t, before, after, "the spans of the baseline and the candidate")
}
