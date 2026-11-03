//go:build unix

package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	evalv1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v1/evaluationv1connect"
	syncv1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/sync/v1/syncv1connect"
	evaluationpb "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	syncpb "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

const characterizedFlags = `{"flags":{"f1":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`

// Every test of this file stops the runtime by ending the context of start, never with a signal.

type ports struct{ management, service, ofrep, sync uint16 }

func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return uint16(l.Addr().(*net.TCPAddr).Port)
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
	rt, err := FromConfig(logger.NewLogger(nil, false), "test", cfg)
	require.NoError(t, err)
	ctx, stop := context.WithCancel(context.Background())
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		result <- rt.start(ctx)
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
	}, 20*time.Second, 50*time.Millisecond, "the runtime becomes ready")
	return stop, result
}

// streamOutcome is what a client stream saw when the context of start ended.
type streamOutcome struct {
	events []string
	err    error
}

// shutdownRun starts a runtime, opens one client stream through open and ends the context of start. It returns what
// the stream saw, start's result and how long start took.
func shutdownRun(t *testing.T, open func(p ports) (next func() (string, error))) (streamOutcome, error, time.Duration) {
	t.Helper()
	p := newPorts(t)
	stop, done := startRuntime(t, characterizedConfig(t, p))
	next := open(p)

	ended := make(chan streamOutcome, 1)
	go func() {
		var o streamOutcome
		for {
			e, err := next()
			if err != nil {
				o.err = err
				ended <- o
				return
			}
			o.events = append(o.events, e)
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
	var out streamOutcome
	select {
	case out = <-ended:
	case <-time.After(3 * time.Second):
		out.err = errors.New("still open")
	}
	return out, startErr, took
}

func TestShutdownEvaluationEventStream(t *testing.T) {
	out, startErr, took := shutdownRun(t, func(p ports) func() (string, error) {
		client := evalv1.NewServiceClient(http.DefaultClient, fmt.Sprintf("http://127.0.0.1:%d", p.service))
		stream, err := client.EventStream(context.Background(), connect.NewRequest(&evaluationpb.EventStreamRequest{}))
		require.NoError(t, err)
		return func() (string, error) {
			if !stream.Receive() {
				return "", stream.Err()
			}
			return stream.Msg().Type, nil
		}
	})
	// The graceful shutdown of the server waits for the open stream until its 5s limit expires, so start
	// fails, and the provider_shutdown notification that follows reaches nobody: the stream gets no event
	// and stays open. The endpoint that sends it itself is PR 3 (3e).
	require.ErrorContains(t, startErr, "error shutting down server: context deadline exceeded")
	require.GreaterOrEqual(t, took, 4*time.Second)
	require.Less(t, took, 8*time.Second)
	require.Empty(t, out.events)
	require.EqualError(t, out.err, "still open")
}

func TestShutdownFlagSyncStream(t *testing.T) {
	out, startErr, took := shutdownRun(t, func(p ports) func() (string, error) {
		client := syncv1.NewFlagSyncServiceClient(http.DefaultClient, fmt.Sprintf("http://127.0.0.1:%d", p.sync))
		stream, err := client.SyncFlags(context.Background(), connect.NewRequest(&syncpb.SyncFlagsRequest{}))
		require.NoError(t, err)
		return func() (string, error) {
			if !stream.Receive() {
				return "", stream.Err()
			}
			return "sync", nil
		}
	})
	// The stream outlives the server's 5s graceful limit and is cut: the client sees a truncated envelope.
	require.NoError(t, startErr)
	require.GreaterOrEqual(t, took, 4*time.Second)
	require.Less(t, took, 8*time.Second)
	require.Equal(t, []string{"sync"}, out.events, "the flag configuration, then nothing")
	require.ErrorContains(t, out.err, "incomplete envelope")
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
