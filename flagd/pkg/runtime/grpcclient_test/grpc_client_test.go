//go:build unix

// Package grpcclient_test pins the client-side telemetry of the flagd-grpc sync provider as FromConfig wires it:
// the span of the sync stream and the rpc.client metrics the gRPC stats handler records. It builds one runtime,
// with the console span exporter, in a test binary of its own; the stream metrics are pinned in syncmetrics_test.
package grpcclient_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	syncv1grpc "buf.build/gen/go/open-feature/flagd/grpc/go/flagd/sync/v1/syncv1grpc"
	v1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature/flagd/flagd/pkg/runtime"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const flagData = `{"flags":{"f1":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`

// syncServer sends one payload per stream and keeps the stream open until the client ends it.
type syncServer struct {
	syncv1grpc.UnimplementedFlagSyncServiceServer
}

func (syncServer) SyncFlags(_ *v1.SyncFlagsRequest, stream syncv1grpc.FlagSyncService_SyncFlagsServer) error {
	if err := stream.Send(&v1.SyncFlagsResponse{FlagConfiguration: flagData}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return nil
}

// consoleSpans redirects file descriptor 1, where the console exporter writes, into a buffer while the test runs.
type consoleSpans struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *consoleSpans) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

type span struct {
	Name                 string
	SpanKind             int
	Attributes           []struct{ Key string }
	InstrumentationScope struct{ Name string }
}

func (c *consoleSpans) spans() []span {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []span
	dec := json.NewDecoder(bytes.NewReader(c.buf.Bytes()))
	for {
		var s span
		// io.EOF, or a span still being written
		if err := dec.Decode(&s); err != nil {
			return out
		}
		out = append(out, s)
	}
}

func redirectStdout(t *testing.T) *consoleSpans {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	saved, err := unix.Dup(1)
	require.NoError(t, err)
	require.NoError(t, unix.Dup2(int(w.Fd()), 1))
	c := &consoleSpans{}
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(c, r)
	}()
	t.Cleanup(func() {
		_ = unix.Dup2(saved, 1)
		_ = unix.Close(saved)
		_ = w.Close()
		<-copied
	})
	return c
}

// rpcClientSeries renders the rpc.client series as name{labels}, sorted, with the server's address masked.
func rpcClientSeries(t *testing.T) string {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	var lines []string
	for _, f := range families {
		if !strings.HasPrefix(f.GetName(), "rpc_client_") {
			continue
		}
		lines = append(lines, "# TYPE "+f.GetName()+" "+f.GetType().String())
		for _, m := range f.GetMetric() {
			var labels []string
			for _, l := range m.GetLabel() {
				value := l.GetValue()
				if l.GetName() == "server_address" || l.GetName() == "server_port" {
					value = "*"
				}
				labels = append(labels, l.GetName()+"="+value)
			}
			slices.Sort(labels)
			lines = append(lines, f.GetName()+"{"+strings.Join(labels, ",")+"}")
		}
	}
	slices.Sort(lines)
	return strings.Join(slices.Compact(lines), "\n") + "\n"
}

// TestGRPCSyncClientInstrumentationCharacterization: a flagd-grpc source against a loopback flag sync server
// records the client span of its SyncFlags stream, ended when the stream ends, and the rpc.client metrics.
func TestGRPCSyncClientInstrumentationCharacterization(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "console")
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "50")
	console := redirectStdout(t)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	syncv1grpc.RegisterFlagSyncServiceServer(server, syncServer{})
	go func() { _ = server.Serve(l) }()
	defer server.Stop()

	rt, err := runtime.FromConfig(logger.NewLogger(nil, false), "test", runtime.Config{
		KeepAliveMinTime:             runtime.KeepAliveMinTimeDefault,
		KeepAlivePermitWithoutStream: runtime.KeepAlivePermitWithoutStreamDefault,
		SyncProviders:                []coresync.SourceConfig{{URI: l.Addr().String(), Provider: "grpc"}},
	}, runtime.Official())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	payloads := make(chan coresync.DataSync, 10)
	stopped := make(chan struct{})
	require.Len(t, rt.Syncs, 1)
	require.NoError(t, rt.Syncs[0].Init(ctx))
	go func() {
		defer close(stopped)
		_ = rt.Syncs[0].Sync(ctx, payloads)
	}()
	select {
	case p := <-payloads:
		require.Equal(t, flagData, p.FlagData)
	case <-time.After(10 * time.Second):
		t.Fatal("the flagd-grpc source delivered nothing")
	}

	// the stream's span ends when the stream does
	cancel()
	<-stopped

	var client []span
	require.Eventually(t, func() bool {
		client = client[:0]
		for _, s := range console.spans() {
			if s.InstrumentationScope.Name == "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc" {
				client = append(client, s)
			}
		}
		return len(client) > 0
	}, 10*time.Second, 50*time.Millisecond, "the client span is exported")
	require.Len(t, client, 1)
	require.Equal(t, "flagd.sync.v1.FlagSyncService/SyncFlags", client[0].Name)
	require.Equal(t, 3, client[0].SpanKind, "a client span")
	var keys []string
	for _, a := range client[0].Attributes {
		keys = append(keys, a.Key)
	}
	slices.Sort(keys)
	require.Equal(t, []string{"rpc.method", "rpc.response.status_code", "rpc.system.name", "server.address", "server.port"}, keys)

	got := rpcClientSeries(t)
	path := filepath.Join("testdata", "rpc_client.golden")
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run go test -update to create %s", path)
	require.Equal(t, string(want), got)
}
