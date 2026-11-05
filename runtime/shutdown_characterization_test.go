//go:build unix

package runtime

import (
	"context"
	"fmt"
	"github.com/open-feature-forking/openfeature-relay/runtime/internal/officialset"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	evalv1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v1/evaluationv1connect"
	syncv1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/sync/v1/syncv1connect"
	evaluationpb "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	syncpb "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/runtime/internal/porttest"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const characterizedFlags = `{"flags":{"f1":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`

// Every test of this file stops the runtime by ending the context of start, never with a signal.

type ports struct{ management, service, ofrep, sync uint16 }

// freePort reserves a port the runtime is later handed through Components.Listen: see porttest.
func freePort(t *testing.T) uint16 {
	t.Helper()
	return porttest.Port(t)
}

// listening makes the runtime open its listeners through porttest, which hands it the ports the test reserved.
func listening(c Components) Components {
	if c.Listen == nil {
		c.Listen = porttest.Listen
	}
	return c
}

func newPorts(t *testing.T) ports {
	return ports{freePort(t), freePort(t), freePort(t), freePort(t)}
}

// characterizedConfig is a Config with one file source, every endpoint on its own free port, and
// OFREP's event stream enabled.
func characterizedConfig(t *testing.T, p ports) Config {
	t.Helper()
	// FromConfig installs a global trace provider unless the exporter is none; the span tests of this
	// package own the global one.
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	file := filepath.Join(t.TempDir(), "flags.json")
	require.NoError(t, os.WriteFile(file, []byte(characterizedFlags), 0o600))
	return Config{
		ManagementPort:               p.management,
		ServicePort:                  p.service,
		OfrepServicePort:             p.ofrep,
		SyncServicePort:              p.sync,
		OfrepSSEEnabled:              true,
		OfrepSSEInactivityDel:        60,
		StreamDeadline:               time.Minute,
		KeepAliveMinTime:             KeepAliveMinTimeDefault,
		KeepAlivePermitWithoutStream: KeepAlivePermitWithoutStreamDefault,
		SyncProviders:                []coresync.SourceConfig{{URI: file, Provider: "file"}},
	}
}

// startRuntime runs the runtime on a context the test cancels, and returns once it reports ready.
func startRuntime(t *testing.T, cfg Config) (cancel func(), done <-chan error) {
	t.Helper()
	// the runtime's pins run without a telemetry provider, as they did when the components were hand-written and the
	// command chose the provider; the tests that need one set it
	components := officialset.Components()
	components.Telemetry = nil
	return startRuntimeWith(t, cfg, components)
}

// startRuntimeWith is startRuntime for a distribution with these components.
func startRuntimeWith(t *testing.T, cfg Config, components Components) (cancel func(), done <-chan error) {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	report := &runReport{logs: logs}
	rt, err := FromConfig(logger.NewLogger(zap.New(core), false), "test", cfg, listening(components))
	require.NoError(t, err)
	ctx, stop := context.WithCancel(context.Background())
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		err := rt.start(ctx)
		report.result.Store(&err)
		result <- err
		close(finished)
	}()
	t.Cleanup(func() {
		stop()
		select {
		case <-finished:
		case <-time.After(20 * time.Second):
		}
	})

	ready := fmt.Sprintf("http://127.0.0.1:%d/readyz", cfg.ManagementPort)
	require.Eventually(t, func() bool {
		resp, err := http.Get(ready) //nolint:gosec,noctx // local test endpoint
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, time.Minute, 50*time.Millisecond, "the runtime becomes ready: %s", report)
	// a port nothing asked for stays closed to clients, as it is when the endpoint is off
	porttest.Release(cfg.ManagementPort, cfg.ServicePort, cfg.OfrepServicePort, cfg.SyncServicePort)
	return stop, result
}

// streamOutcome is what a client stream saw by the time start returned and the stream had its moment to end.
type streamOutcome struct {
	events []string
	// err is why the stream ended, nil while it is still open
	err error
	// open is whether the stream was still open then
	open bool
}

// shutdownRun starts a runtime, opens one client stream through open and ends the context of start. It returns what
// the stream saw, start's result and how long start took. The events are read while the stream is open, so a
// stream that never ends still reports what it received.
func shutdownRun(t *testing.T, open func(p ports) (next func() (string, error))) (streamOutcome, error, time.Duration) {
	t.Helper()
	p := newPorts(t)
	stop, done := startRuntime(t, characterizedConfig(t, p))
	next := open(p)

	var mu sync.Mutex
	var seen []string
	var endErr error
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		for {
			e, err := next()
			mu.Lock()
			if err != nil {
				endErr = err
				mu.Unlock()
				return
			}
			seen = append(seen, e)
			mu.Unlock()
		}
	}()
	time.Sleep(300 * time.Millisecond)

	began := time.Now()
	stop()
	var startErr error
	select {
	case startErr = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("start did not return")
	}
	took := time.Since(began)

	var stillOpen bool
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		stillOpen = true
	}
	mu.Lock()
	defer mu.Unlock()
	return streamOutcome{events: append([]string(nil), seen...), err: endErr, open: stillOpen}, startErr, took
}

func TestShutdownEvaluationEventStream(t *testing.T) {
	out, startErr, took := shutdownRun(t, func(p ports) func() (string, error) {
		client := evalv1.NewServiceClient(http.DefaultClient, fmt.Sprintf("http://127.0.0.1:%d", p.service))
		stream, err := client.EventStream(context.Background(), connect.NewRequest(&evaluationpb.EventStreamRequest{}))
		require.NoError(t, err)
		return func() (string, error) {
			if !stream.Receive() {
				if err := stream.Err(); err != nil {
					return "", err
				}
				return "", io.EOF
			}
			return stream.Msg().Type, nil
		}
	})
	// When its context ends the endpoint sends provider_shutdown to the open stream and ends it, so the server shuts
	// down at once and start returns nil.
	require.NoError(t, startErr)
	require.Less(t, took, 2*time.Second)
	require.Equal(t, []string{"provider_ready", "provider_shutdown"}, out.events)
	require.False(t, out.open, "the stream is ended")
	require.ErrorIs(t, out.err, io.EOF)
}

func TestShutdownFlagSyncStream(t *testing.T) {
	out, startErr, took := shutdownRun(t, func(p ports) func() (string, error) {
		client := syncv1.NewFlagSyncServiceClient(http.DefaultClient, fmt.Sprintf("http://127.0.0.1:%d", p.sync))
		stream, err := client.SyncFlags(context.Background(), connect.NewRequest(&syncpb.SyncFlagsRequest{}))
		require.NoError(t, err)
		return func() (string, error) {
			if !stream.Receive() {
				if err := stream.Err(); err != nil {
					return "", err
				}
				return "", io.EOF
			}
			return "sync", nil
		}
	})
	// The endpoint ends its streams when its context ends, so the server shuts down at once.
	require.NoError(t, startErr)
	require.Less(t, took, 2*time.Second)
	require.Equal(t, []string{"sync"}, out.events, "the flag configuration, then the end")
	require.ErrorIs(t, out.err, io.EOF)
	require.False(t, out.open)
}

func TestShutdownOFREPEventStream(t *testing.T) {
	out, startErr, took := shutdownRun(t, func(p ports) func() (string, error) {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/sse", p.ofrep), nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { resp.Body.Close() })
		buf := make([]byte, 4096)
		return func() (string, error) {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				return string(buf[:n]), nil
			}
			return "", err
		}
	})
	// The handler ends with the context, so the connection closes at once.
	require.NoError(t, startErr)
	require.Less(t, took, 2*time.Second)
	require.Empty(t, out.events)
	require.ErrorContains(t, out.err, "EOF", "the body ends, cleanly or truncated")
}
