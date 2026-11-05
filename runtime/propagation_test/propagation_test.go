//go:build unix

// Package propagation_test pins trace propagation into flagd's server spans. It builds one runtime through
// FromConfig, with the console span exporter, in a test binary of its own: FromConfig installs process-wide
// telemetry, and the exporter writes to the process's standard output. The management port characterization pins
// that the propagators are installed with OTEL_TRACES_EXPORTER unset too, since autoexport then selects OTLP.
// Run it with -count=1.
//
// The test starts the runtime and stops it with SIGTERM, which Start handles itself.
package propagation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/open-feature-forking/openfeature-relay/runtime/internal/officialset"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/runtime"
	otelprovider "github.com/open-feature-forking/openfeature-relay/telemetry/otel"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	flags       = `{"flags":{"f1":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`
	traceID     = "0af7651916cd43dd8448eb211c80319c"
	parentID    = "b7ad6b7169203331"
	traceparent = "00-" + traceID + "-" + parentID + "-01"
)

// issuedPorts are the ports freePort has handed out in this process. A port is free again the moment it is closed, so
// the next call can get the same one, and one start would listen twice on it.
var issuedPorts sync.Map

func freePort(t *testing.T) uint16 {
	t.Helper()
	// a port the kernel hands out again stays open until a new one comes, so the next listen cannot get it
	var held []net.Listener
	defer func() {
		for _, h := range held {
			_ = h.Close()
		}
	}()
	for {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := l.Addr().(*net.TCPAddr).Port
		if _, taken := issuedPorts.LoadOrStore(port, true); taken {
			held = append(held, l)
			continue
		}
		_ = l.Close()
		return uint16(port)
	}
}

// span is the part of the console exporter's JSON the test reads.
type span struct {
	Name        string
	SpanContext struct{ TraceID, SpanID string }
	Parent      struct {
		TraceID, SpanID string
		Remote          bool
	}
	InstrumentationScope struct{ Name string }
}

// spans collects what the console exporter writes to standard output.
type spans struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *spans) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *spans) all(t *testing.T) []span {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []span
	dec := json.NewDecoder(bytes.NewReader(s.buf.Bytes()))
	for {
		var sp span
		// io.EOF, or a span still being written
		if err := dec.Decode(&sp); err != nil {
			return out
		}
		out = append(out, sp)
	}
}

// TestTracePropagationCharacterization: with a span exporter, an incoming traceparent parents the server span of
// a ConnectRPC call, an OFREP call and the flag sync flags route. Standard output stays redirected to the console
// exporter for the whole test, since the exporter is created after FromConfig returns.
func TestTracePropagationCharacterization(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "console")
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "50")

	// the console exporter writes to the os.Stdout of its package's initialization, so file descriptor 1 itself is
	// redirected while the test runs
	r, w, err := os.Pipe()
	require.NoError(t, err)
	saved, err := unix.Dup(1)
	require.NoError(t, err)
	require.NoError(t, unix.Dup2(int(w.Fd()), 1))
	collected := &spans{}
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(collected, r)
	}()
	t.Cleanup(func() {
		_ = unix.Dup2(saved, 1)
		_ = unix.Close(saved)
		_ = w.Close()
		<-copied
	})

	file := filepath.Join(t.TempDir(), "flags.json")
	require.NoError(t, os.WriteFile(file, []byte(flags), 0o600))
	servicePort, ofrepPort, syncPort := freePort(t), freePort(t), freePort(t)
	rt, err := runtime.FromConfig(logger.NewLogger(nil, false), "test", runtime.Config{
		ManagementPort:               freePort(t),
		ServicePort:                  servicePort,
		OfrepServicePort:             ofrepPort,
		SyncServicePort:              syncPort,
		SyncServiceHTTPEnabled:       true,
		StreamDeadline:               5 * time.Second,
		KeepAliveMinTime:             runtime.KeepAliveMinTimeDefault,
		KeepAlivePermitWithoutStream: runtime.KeepAlivePermitWithoutStreamDefault,
		SyncProviders:                []coresync.SourceConfig{{URI: file, Provider: "file"}},
	}, withOpenTelemetry(officialset.Components()))
	require.NoError(t, err)

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

	send := func(method, url, body, contentType string) (int, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
		if err != nil {
			return 0, err
		}
		req.Header.Set("traceparent", traceparent)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		return resp.StatusCode, nil
	}
	calls := []struct{ method, url, body, contentType string }{
		{http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/flagd.evaluation.v1.Service/ResolveBoolean", servicePort), `{"flagKey":"f1"}`, "application/json"},
		{http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/evaluate/flags/f1", ofrepPort), `{"context":{}}`, "application/json"},
		{http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1/flags", syncPort), "", ""},
	}
	for _, c := range calls {
		require.Eventually(t, func() bool {
			code, err := send(c.method, c.url, c.body, c.contentType)
			return err == nil && code == http.StatusOK
		}, 20*time.Second, 100*time.Millisecond, "%s %s answers", c.method, c.url)
	}

	// the server span of each call, as scope/name, parented by the incoming span; a served request's otelhttp span
	// is named after its method and the matched route
	want := []string{
		"connectrpc.com/otelconnect/flagd.evaluation.v1.Service/ResolveBoolean",
		"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp/POST /",
		"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp/GET /v1/flags",
	}
	got := map[string]bool{}
	require.Eventually(t, func() bool {
		for _, sp := range collected.all(t) {
			if sp.Parent.TraceID == traceID && sp.Parent.SpanID == parentID && sp.Parent.Remote &&
				sp.SpanContext.TraceID == traceID {
				got[sp.InstrumentationScope.Name+"/"+sp.Name] = true
			}
		}
		return len(got) >= len(want)
	}, 10*time.Second, 50*time.Millisecond, "the server spans are exported")
	require.ElementsMatch(t, want, slices.Collect(maps.Keys(got)))
}

// withOpenTelemetry is the components of flagd-full with the OpenTelemetry telemetry provider, as flagd's command
// line selects it.
func withOpenTelemetry(components runtime.Components) runtime.Components {
	components.Telemetry = otelprovider.NewFactory()
	return components
}
