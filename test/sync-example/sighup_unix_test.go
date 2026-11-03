//go:build unix

package syncexample

import (
	"context"
	"os"
	ossignal "os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

// TestSIGHUPSignals runs the trigger on the process's own SIGHUP.
func TestSIGHUPSignals(t *testing.T) {
	// a SIGHUP that arrives before the trigger listens must not end the test process
	guard := make(chan os.Signal, 8)
	ossignal.Notify(guard, syscall.SIGHUP)
	defer ossignal.Stop(guard)

	trigger, err := TriggerFactory{}.CreateTrigger(sync.Settings{})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan struct{}, 4)
	done := make(chan error, 1)
	go func() { done <- trigger.Run(ctx, func() { signals <- struct{}{} }) }()

	// the trigger subscribes when it starts, so the signal is sent until it is seen
	require.Eventually(t, func() bool {
		require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGHUP))
		select {
		case <-signals:
			return true
		case <-time.After(50 * time.Millisecond):
			return false
		}
	}, 5*time.Second, time.Millisecond)

	cancel()
	require.NoError(t, <-done)
}
