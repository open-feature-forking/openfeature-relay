//go:build unix

// Package managementport pins what the runtime serves on the management port. It is its own test
// binary because the Prometheus exporter registers with the default registry once per process; no
// other test scrapes /metrics. Run it with -count=1: a second run in the same process fails on that registration.
//
// The test starts the runtime and stops it with SIGTERM, which Start handles itself.
package managementport

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	otelprovider "github.com/open-feature-forking/openfeature-relay/telemetry/otel"
	"github.com/open-feature/flagd/flagd/pkg/runtime"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
)

const flags = `{"flags":{"f1":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`

func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return uint16(l.Addr().(*net.TCPAddr).Port)
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec,noctx // local test endpoint
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(body)
}

// TestManagementPort builds one runtime through FromConfig with a file source and an HTTP source whose
// first delivery is held back, so readiness can be observed before and after every source has delivered.
func TestManagementPort(t *testing.T) {
	file := filepath.Join(t.TempDir(), "flags.json")
	require.NoError(t, os.WriteFile(file, []byte(flags), 0o600))

	release := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(flags))
	}))
	defer remote.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	managementPort, servicePort, ofrepPort, syncPort := freePort(t), freePort(t), freePort(t), freePort(t)
	rt, err := runtime.FromConfig(logger.NewLogger(nil, false), "test", runtime.Config{
		ManagementPort:               managementPort,
		ServicePort:                  servicePort,
		OfrepServicePort:             ofrepPort,
		SyncServicePort:              syncPort,
		SyncServiceHTTPEnabled:       true,
		StreamDeadline:               5 * time.Second,
		KeepAliveMinTime:             runtime.KeepAliveMinTimeDefault,
		KeepAlivePermitWithoutStream: runtime.KeepAlivePermitWithoutStreamDefault,
		SyncProviders: []coresync.SourceConfig{
			{URI: file, Provider: "file"},
			{URI: remote.URL, Provider: "http", Interval: 60},
		},
	}, withOpenTelemetry(runtime.Official()))
	require.NoError(t, err)
	// with OTEL_TRACES_EXPORTER unset, autoexport selects the OTLP span exporter, so FromConfig installs the trace
	// provider and the W3C propagators; only OTEL_TRACES_EXPORTER=none leaves both noop
	require.ElementsMatch(t, []string{"traceparent", "tracestate", "baggage"}, otel.GetTextMapPropagator().Fields())

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

	management := fmt.Sprintf("http://127.0.0.1:%d", managementPort)
	require.Eventually(t, func() bool {
		resp, err := http.Get(management + "/healthz") //nolint:gosec,noctx // local test endpoint
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 20*time.Second, 50*time.Millisecond, "healthz answers once the runtime serves")

	resp, _ := get(t, management+"/readyz")
	require.Equal(t, http.StatusPreconditionFailed, resp.StatusCode, "not ready while a source has not delivered")

	close(release)
	require.Eventually(t, func() bool {
		resp, err := http.Get(management + "/readyz") //nolint:gosec,noctx // local test endpoint
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 20*time.Second, 50*time.Millisecond, "ready once every source has delivered")

	resp, _ = get(t, management+"/healthz")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, body := get(t, management+"/metrics")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain; version=0.0.4"), resp.Header.Get("Content-Type"))
	for _, name := range []string{
		"target_info",
		"feature_flag_flagd_sync_client_flag_config_received_total",
		"feature_flag_flagd_sync_client_flag_config_last_received_timestamp_seconds",
		"go_goroutines",
	} {
		require.Contains(t, body, "# TYPE "+name+" ", "flagd's metric %s is exposed", name)
	}

	for _, path := range []string{"/metrics", "/healthz", "/readyz"} {
		resp, _ := get(t, fmt.Sprintf("http://127.0.0.1:%d%s", servicePort, path))
		require.Equal(t, http.StatusNotFound, resp.StatusCode, "%s is served on the management port only", path)
	}

	t.Run("inventory", func(t *testing.T) {
		callEveryEndpoint(t, servicePort, ofrepPort, syncPort)
		_, body := get(t, management+"/metrics")
		checkGolden(t, "inventory.golden", inventory(body, file, remote.URL))
	})
}

// withOpenTelemetry is the components of flagd-full with the OpenTelemetry telemetry provider, as flagd's command
// line selects it.
func withOpenTelemetry(components runtime.Components) runtime.Components {
	components.Telemetry = otelprovider.NewFactory()
	return components
}
