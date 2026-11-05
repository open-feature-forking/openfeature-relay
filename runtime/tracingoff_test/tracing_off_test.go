//go:build unix

// Package tracingoff_test pins flagd with tracing turned off. It builds one runtime through FromConfig, with
// OTEL_TRACES_EXPORTER=none, in a test binary of its own, since FromConfig installs process-wide telemetry: run it
// with -count=1.
//
// The test starts the runtime and stops it with SIGTERM, which Start handles itself.
package tracingoff_test

import (
	"context"
	"fmt"
	"github.com/open-feature-forking/openfeature-relay/runtime/internal/officialset"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/runtime"
	otelprovider "github.com/open-feature-forking/openfeature-relay/telemetry/otel"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	flags       = `{"flags":{"f1":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`
	traceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
)

func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return uint16(l.Addr().(*net.TCPAddr).Port)
}

// TestTracingOffCharacterization: with OTEL_TRACES_EXPORTER=none, FromConfig installs neither a trace provider nor
// the propagators. An incoming traceparent is extracted into nothing and the spans flagd starts do not record, so no
// server span is parented by the incoming trace; requests are served as usual.
func TestTracingOffCharacterization(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")

	file := filepath.Join(t.TempDir(), "flags.json")
	require.NoError(t, os.WriteFile(file, []byte(flags), 0o600))
	servicePort := freePort(t)
	rt, err := runtime.FromConfig(logger.NewLogger(nil, false), "test", runtime.Config{
		ManagementPort:               freePort(t),
		ServicePort:                  servicePort,
		OfrepServicePort:             freePort(t),
		SyncServicePort:              freePort(t),
		StreamDeadline:               5 * time.Second,
		KeepAliveMinTime:             runtime.KeepAliveMinTimeDefault,
		KeepAlivePermitWithoutStream: runtime.KeepAlivePermitWithoutStreamDefault,
		SyncProviders:                []coresync.SourceConfig{{URI: file, Provider: "file"}},
	}, withOpenTelemetry(officialset.Components()))
	require.NoError(t, err)

	require.Empty(t, otel.GetTextMapPropagator().Fields(), "no propagator is installed")
	carrier := propagation.MapCarrier{"traceparent": traceparent}
	ctx := otel.GetTextMapPropagator().Extract(context.Background(), carrier)
	require.False(t, trace.SpanContextFromContext(ctx).IsValid(), "an incoming traceparent is extracted into nothing")
	_, span := otel.Tracer("tracingoff_test").Start(ctx, "probe")
	require.False(t, span.IsRecording(), "spans do not record")
	span.End()

	done := make(chan error, 1)
	go func() { done <- rt.Start() }()
	defer func() {
		require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(20 * time.Second):
			t.Fatal("the runtime did not stop")
		}
	}()

	require.Eventually(t, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			fmt.Sprintf("http://127.0.0.1:%d/flagd.evaluation.v1.Service/ResolveBoolean", servicePort),
			strings.NewReader(`{"flagKey":"f1"}`))
		if err != nil {
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("traceparent", traceparent)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 20*time.Second, 100*time.Millisecond, "a ConnectRPC call with a traceparent is served")
}

// withOpenTelemetry is the components of flagd-full with the OpenTelemetry telemetry provider, as flagd's command
// selects it.
func withOpenTelemetry(components runtime.Components) runtime.Components {
	components.Telemetry = otelprovider.NewFactory()
	return components
}
