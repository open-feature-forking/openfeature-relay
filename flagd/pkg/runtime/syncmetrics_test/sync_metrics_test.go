//go:build unix

// Package syncmetrics_test pins the sync instruments flagd-full emits, as Prometheus exposes them. It
// builds a runtime through runtime.FromConfig, which installs process-wide telemetry, so it is a test
// binary of its own and calls FromConfig exactly once.
package syncmetrics_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"buf.build/gen/go/open-feature/flagd/grpc/go/flagd/sync/v1/syncv1grpc"
	v1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"github.com/open-feature/flagd/core/pkg/logger"
	coresync "github.com/open-feature/flagd/core/pkg/sync"
	"github.com/open-feature/flagd/flagd/pkg/runtime"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

const (
	syncmetricsScope = "github.com/open-feature/flagd/core/pkg/sync/syncmetrics"
	grpcScope        = "github.com/open-feature/flagd/core/pkg/sync/grpc"
	flagData         = `{"flags":{"f1":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`
)

// syncServer sends one payload per stream and keeps the stream open until the server stops.
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

func (syncServer) FetchAllFlags(context.Context, *v1.FetchAllFlagsRequest) (*v1.FetchAllFlagsResponse, error) {
	return &v1.FetchAllFlagsResponse{FlagConfiguration: flagData}, nil
}

func serve(t *testing.T, addr string) (*grpc.Server, string) {
	t.Helper()
	var l net.Listener
	var err error
	// the port of a stopped server can take a moment to become available again
	require.Eventually(t, func() bool {
		l, err = net.Listen("tcp", addr)
		return err == nil
	}, 3*time.Second, 50*time.Millisecond, "listen on %s: %v", addr, err)
	server := grpc.NewServer()
	syncv1grpc.RegisterFlagSyncServiceServer(server, syncServer{})
	go func() { _ = server.Serve(l) }()
	return server, l.Addr().String()
}

// series is one Prometheus time series, rendered as name{labels} for comparison.
type series struct {
	name   string
	typ    string
	labels map[string]string
	value  float64
}

func gather(t *testing.T) []series {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	var out []series
	for _, f := range families {
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			s := series{name: f.GetName(), typ: f.GetType().String(), labels: labels}
			switch f.GetType() {
			case dto.MetricType_COUNTER:
				s.value = m.GetCounter().GetValue()
			case dto.MetricType_GAUGE:
				s.value = m.GetGauge().GetValue()
			case dto.MetricType_HISTOGRAM:
				s.value = float64(m.GetHistogram().GetSampleCount())
			}
			out = append(out, s)
		}
	}
	return out
}

func TestSyncMetricsCharacterization(t *testing.T) {
	server, addr := serve(t, "127.0.0.1:0")
	defer func() { server.Stop() }()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "flags.json")
	require.NoError(t, os.WriteFile(filePath, []byte(flagData), 0o600))

	rt, err := runtime.FromConfig(logger.NewLogger(nil, false), "test", runtime.Config{
		KeepAliveMinTime:             runtime.KeepAliveMinTimeDefault,
		KeepAlivePermitWithoutStream: runtime.KeepAlivePermitWithoutStreamDefault,
		SyncProviders: []coresync.SourceConfig{
			{URI: filePath, Provider: "file"},
			{URI: addr, Provider: "grpc"},
		},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	payloads := make(chan coresync.DataSync, 100)
	go func() {
		for {
			select {
			case <-payloads:
			case <-ctx.Done():
				return
			}
		}
	}()
	for _, s := range rt.Syncs {
		require.NoError(t, s.Init(ctx))
		go func() { _ = s.Sync(ctx, payloads) }()
	}

	// every payload is on its way through the channel once these counters are in place
	require.Eventually(t, func() bool { return strings.Contains(render(pick(gather(t)), filePath), "stream_active") }, 10*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool {
		return strings.Count(render(pick(gather(t)), filePath), "flag_config_received_total") == 2
	}, 10*time.Second, 100*time.Millisecond)
	require.Equal(t, strings.Join(wantBefore, "\n"), render(pick(gather(t)), filePath))

	// force a reconnect: stop the server and bring it back on the same address, within the first
	// backoff step (backOffBase^1 seconds)
	server.Stop()
	server, _ = serve(t, addr)
	require.Eventually(t, func() bool {
		return strings.Contains(render(pick(gather(t)), filePath), "stream_reconnects_total")
	}, 20*time.Second, 200*time.Millisecond, "the stream reconnects")
	require.Equal(t, strings.Join(wantAfter, "\n"), render(pick(gather(t)), filePath))
}

const (
	fileLabels  = "feature_flag_flagd_sync_type=file,feature_flag_flagd_sync_uri=<file>,flagd_sync_provider=file,flagd_sync_uri=<file>"
	grpcLabels  = "feature_flag_flagd_sync_type=grpc,feature_flag_flagd_sync_uri=,flagd_sync_provider=grpc,flagd_sync_uri="
	syncScope   = "otel_scope_name=" + syncmetricsScope
	streamScope = "otel_scope_name=" + grpcScope
)

// The sync client URI of a gRPC source is recorded sanitized, so it is empty for a host:port target.
// Values of timestamps and of the rpc_client_* series vary and are not rendered.
var wantBefore = []string{
	"feature_flag_flagd_sync_client_flag_config_last_received_timestamp_seconds GAUGE {" + fileLabels + "," + syncScope + "} -",
	"feature_flag_flagd_sync_client_flag_config_last_received_timestamp_seconds GAUGE {" + grpcLabels + "," + syncScope + "} -",
	"feature_flag_flagd_sync_client_flag_config_received_total COUNTER {" + fileLabels + "," + syncScope + "} 1",
	"feature_flag_flagd_sync_client_flag_config_received_total COUNTER {" + grpcLabels + "," + syncScope + "} 1",
	"feature_flag_flagd_sync_client_stream_active GAUGE {" + grpcLabels + "," + streamScope + "} 1",
}

var wantAfter = []string{
	"feature_flag_flagd_sync_client_flag_config_last_received_timestamp_seconds GAUGE {" + fileLabels + "," + syncScope + "} -",
	"feature_flag_flagd_sync_client_flag_config_last_received_timestamp_seconds GAUGE {" + grpcLabels + "," + syncScope + "} -",
	"feature_flag_flagd_sync_client_flag_config_received_total COUNTER {" + fileLabels + "," + syncScope + "} 1",
	"feature_flag_flagd_sync_client_flag_config_received_total COUNTER {" + grpcLabels + "," + syncScope + "} 2",
	"feature_flag_flagd_sync_client_stream_active GAUGE {" + grpcLabels + "," + streamScope + "} 1",
	"feature_flag_flagd_sync_client_stream_reconnects_total COUNTER {" + grpcLabels + "," + streamScope + "} 1",
	"rpc_client_call_duration_seconds HISTOGRAM {otelgrpc} -",
}

func pick(all []series) []series {
	var out []series
	for _, s := range all {
		scope := s.labels["otel_scope_name"]
		if scope == syncmetricsScope || scope == grpcScope || strings.HasPrefix(s.name, "rpc_client") {
			out = append(out, s)
		}
	}
	return out
}

// render gives a stable text for the selected series: sorted, without scope versions and schema
// URLs, with the file source's path replaced, and one line per rpc_client_* family.
func render(all []series, filePath string) string {
	lines := map[string]bool{}
	for _, s := range all {
		if strings.HasPrefix(s.name, "rpc_client") {
			lines[fmt.Sprintf("%s %s {otelgrpc} -", s.name, s.typ)] = true
			continue
		}
		var ls []string
		for k, v := range s.labels {
			if k == "otel_scope_schema_url" || k == "otel_scope_version" {
				continue
			}
			ls = append(ls, k+"="+v)
		}
		sort.Strings(ls)
		value := fmt.Sprint(s.value)
		if strings.HasSuffix(s.name, "_timestamp_seconds") {
			value = "-"
		}
		lines[strings.ReplaceAll(fmt.Sprintf("%s %s {%s} %s", s.name, s.typ, strings.Join(ls, ","), value), filePath, "<file>")] = true
	}
	out := make([]string, 0, len(lines))
	for l := range lines {
		out = append(out, l)
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}
