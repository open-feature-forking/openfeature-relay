package sync

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	syncv1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// answers reports whether the endpoint answers a flag fetch within a second; before the service serves,
// the connection is refused.
func answers(t *testing.T, port int) bool {
	t.Helper()
	client := connectClient(t, fmt.Sprintf("http://localhost:%d", port), "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := client.FetchAllFlags(ctx, connect.NewRequest(&syncv1.FetchAllFlagsRequest{}))
	return err == nil
}

// TestServingWaitsForEverySource pins that the service serves only once every source delivered once.
func TestServingWaitsForEverySource(t *testing.T) {
	const port = 18080

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	svc, _, _, _ := startSyncService(t, ctx, SvcConfigurations{Port: port})

	svc.Emit(testSource1)
	time.Sleep(time.Second)
	require.False(t, answers(t, port), "serving with one of two sources delivered")

	svc.Emit(testSource2)
	require.Eventually(t, func() bool { return answers(t, port) }, 3*time.Second, 50*time.Millisecond,
		"serving once every source delivered")
}

// TestServingStartsAfterFiveSeconds pins that a source that never delivers holds serving back for 5 s, not longer.
func TestServingStartsAfterFiveSeconds(t *testing.T) {
	const port = 18081

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	began := time.Now()
	startSyncService(t, ctx, SvcConfigurations{Port: port})

	time.Sleep(3 * time.Second)
	require.False(t, answers(t, port), "serving after 3 s with no source delivered")

	require.Eventually(t, func() bool { return answers(t, port) }, 4*time.Second, 100*time.Millisecond,
		"serving after the timeout")
	require.GreaterOrEqual(t, time.Since(began), 5*time.Second)
}

// TestCancelledStartStopsWithoutWaiting: a start whose context ends while no source has delivered, as when another
// endpoint fails to bind, stops at once, without the timeout warning and without serving.
func TestCancelledStartStopsWithoutWaiting(t *testing.T) {
	// the service is handed an open listener on a free port, so no other process can hold the port the test asserts on
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	port := listener.Addr().(*net.TCPAddr).Port
	core, logs := observer.New(zap.WarnLevel)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, _, _, done := startSyncService(t, ctx, SvcConfigurations{
		Port: uint16(port), Logger: logger.NewLogger(zap.New(core), false),
		Listen: func(string, string) (net.Listener, error) { return listener, nil },
	})

	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the start did not stop within 2 s of its context ending")
	}
	require.Zero(t, logs.FilterMessageSnippet("timeout while waiting for all sync sources").Len())
	require.False(t, answers(t, port), "serving after the start stopped")
}

// TestLastModifiedMovesOnUnchangedRedelivery pins that the service stamps Last-Modified on every Emit, an unchanged
// redelivery included. The runtime no longer emits for a delivery that changes nothing: the endpoint emits for
// the configuration change events (TestLastModifiedFollowsConfigurationChangeEvents).
func TestLastModifiedMovesOnUnchangedRedelivery(t *testing.T) {
	const port = 18082

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	svc, _, emit, _ := startSyncService(t, ctx, SvcConfigurations{Port: port, HTTPEnabled: true})
	emit()

	url := fmt.Sprintf("http://localhost:%d%s", port, flagsPath)
	client := flagsClient(t, "")
	first := getWithRetry(t, client, url)
	first.Body.Close()
	before := first.Header.Get("Last-Modified")
	require.NotEmpty(t, before)

	time.Sleep(1100 * time.Millisecond) // Last-Modified has a resolution of one second
	svc.Emit(testSource1)               // the store holds the same flags as before

	second, err := client.Get(url)
	require.NoError(t, err)
	second.Body.Close()
	require.NotEqual(t, before, second.Header.Get("Last-Modified"))
	require.Equal(t, first.Header.Get("ETag"), second.Header.Get("ETag"), "the flags did not change")
}
