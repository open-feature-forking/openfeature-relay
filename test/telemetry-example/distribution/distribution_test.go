//go:build unix

// Package distribution_test runs flagd-full's service endpoints with the example telemetry provider instead of the
// OpenTelemetry one (UC-006 main scenario, step 6 T6a). It is a module of its own, so that the example module needs
// the core only. It builds one runtime and stops it with SIGTERM, which Start handles itself.
package distribution_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	flagdcomponents "github.com/open-feature/flagd/flagd/components"
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
	telemetryexample "github.com/open-feature/flagd/test/telemetry-example"
	"github.com/stretchr/testify/require"
)

const flags = `{"flags":{"f1":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`

func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return uint16(l.Addr().(*net.TCPAddr).Port)
}

// TestExtensionTelemetryProvider builds flagd-full's components with the example telemetry provider and makes one
// OFREP evaluation. The provider records the evaluation's spans and the HTTP server metrics, and writes them when the
// runtime shuts down.
func TestExtensionTelemetryProvider(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "flags.json")
	require.NoError(t, os.WriteFile(source, []byte(flags), 0o600))
	out := filepath.Join(dir, "telemetry.jsonl")

	components := flagdcomponents.Components()
	components.Telemetry = telemetryexample.Factory{}
	components.TelemetryConfig = map[string]any{telemetryexample.SettingPath: out}
	ofrepPort := freePort(t)
	rt, err := runtime.FromConfig(logger.NewLogger(nil, false), "test", runtime.Config{
		ManagementPort:               freePort(t),
		ServicePort:                  freePort(t),
		OfrepServicePort:             ofrepPort,
		SyncServicePort:              freePort(t),
		StreamDeadline:               5 * time.Second,
		KeepAliveMinTime:             runtime.KeepAliveMinTimeDefault,
		KeepAlivePermitWithoutStream: runtime.KeepAlivePermitWithoutStreamDefault,
		SyncProviders:                []coresync.SourceConfig{{URI: source, Provider: "file"}},
	}, components)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- rt.Start() }()

	url := fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/evaluate/flags/f1", ofrepPort)
	require.Eventually(t, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`{"context":{}}`))
		if err != nil {
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 20*time.Second, 50*time.Millisecond, "OFREP evaluates f1")

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("the runtime did not stop")
	}

	raw, err := os.ReadFile(out)
	require.NoError(t, err, "the provider writes its file on shutdown")
	recorded := map[string]telemetryexample.Line{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		var l telemetryexample.Line
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &l))
		recorded[l.Type+" "+l.Scope+"/"+l.Name] = l
	}

	// the service endpoint's HTTP instrumentation and the evaluation
	server := recorded["span telemetryexample/http/flagd.ofrep"]
	require.Equal(t, "server", server.Kind)
	require.Equal(t, "/ofrep/v1/evaluate/flags/f1", server.Attributes["url.path"])
	require.EqualValues(t, http.StatusOK, server.Attributes["http.response.status_code"])
	require.Equal(t, "internal", recorded["span jsonEvaluator/resolveAnyValue"].Kind)
	duration := recorded["metric telemetryexample/http/http.server.request.duration"]
	require.Equal(t, "/ofrep/v1/evaluate/flags/{key}", duration.Attributes["http.route"])
	require.Equal(t, "200", duration.Attributes["http.response.status_code"])
	require.EqualValues(t, 1, duration.Count)
	impression := recorded["metric flagd/feature_flag.flagd.impression"]
	require.Equal(t, "f1", impression.Attributes["feature_flag.key"])
	require.EqualValues(t, 1, impression.Value)
	require.Contains(t, recorded, "metric flagd/feature_flag.flagd.result.reason")

	// the runtime and the sync provider record through the same provider
	require.Contains(t, recorded, "span jsonEvaluator/flagSync")
	require.Contains(t, recorded,
		"metric github.com/open-feature/flagd/core/pkg/sync/syncmetrics/feature_flag.flagd.sync.client.flag_config.received")
}
