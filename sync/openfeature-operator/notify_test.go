package ofoperator

import (
	"context"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
)

// TestNotifyReturnsWithPendingEvent pins that notify returns once ctx ends, even when an informer handler's
// event is pending and nobody reads the notifier channel.
func TestNotifyReturnsWithPendingEvent(t *testing.T) {
	fc := fake.NewSimpleDynamicClient(scheme.Scheme, featureFlag("1", simpleFlags("on")))
	s := NewK8sSync(logger.NewLogger(nil, false), opURI, fc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, s.Init(ctx))

	c := make(chan INotify, 1) // as Sync makes it; nobody reads
	done := make(chan struct{})
	go func() { s.notify(ctx, c); close(done) }()
	time.Sleep(500 * time.Millisecond) // Ready fills the buffer, the Add handler blocks behind it
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("notify did not return after ctx ended")
	}
}
