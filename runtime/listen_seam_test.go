//go:build unix

package runtime

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/runtime/runtimetest"
	"github.com/stretchr/testify/require"
)

// recordingListen opens the listeners as net.Listen does and records which it was asked for.
type recordingListen struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingListen) listen(network, address string) (net.Listener, error) {
	r.mu.Lock()
	r.calls = append(r.calls, network+" "+address)
	r.mu.Unlock()
	return net.Listen(network, address)
}

func (r *recordingListen) opened() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]string(nil), r.calls...)
	sort.Strings(out)
	return out
}

// TestListenSeamUsedByEveryEndpoint pins that every listener of the runtime and of the official endpoints goes through
// Components.Listen (step 13 Q8): the flag evaluation, flag sync, OFREP and management ports, and unix sockets in their
// place.
func TestListenSeamUsedByEveryEndpoint(t *testing.T) {
	t.Run("ports", func(t *testing.T) {
		p := newPorts(t)
		rec := &recordingListen{}
		components := runtimetest.Components()
		components.Telemetry = nil
		components.Listen = rec.listen
		startRuntimeWith(t, characterizedConfig(t, p), components)

		want := []string{
			fmt.Sprintf("tcp :%d", p.management), fmt.Sprintf("tcp :%d", p.ofrep),
			fmt.Sprintf("tcp :%d", p.service), fmt.Sprintf("tcp :%d", p.sync),
		}
		sort.Strings(want)
		require.Equal(t, want, rec.opened())
	})

	t.Run("unix sockets", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "listen")
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(dir) })
		p := newPorts(t)
		cfg := characterizedConfig(t, p)
		cfg.ServiceSocketPath = filepath.Join(dir, "evaluation.sock")
		cfg.SyncServiceSocketPath = filepath.Join(dir, "sync.sock")
		rec := &recordingListen{}
		components := runtimetest.Components()
		components.Telemetry = nil
		components.Listen = rec.listen
		startRuntimeWith(t, cfg, components)

		want := []string{
			fmt.Sprintf("tcp :%d", p.management), fmt.Sprintf("tcp :%d", p.ofrep),
			"unix " + cfg.ServiceSocketPath, "unix " + cfg.SyncServiceSocketPath,
		}
		sort.Strings(want)
		require.Equal(t, want, rec.opened())
	})
}

// TestListenSeamRefusalStopsTheRuntime pins that a listener the Listen function refuses ends the runtime with that
// error, as a port in use does.
func TestListenSeamRefusalStopsTheRuntime(t *testing.T) {
	p := newPorts(t)
	components := runtimetest.Components()
	components.Telemetry = nil
	components.Listen = func(network, address string) (net.Listener, error) {
		if strings.HasSuffix(address, fmt.Sprintf(":%d", p.sync)) {
			return nil, fmt.Errorf("listen refused: %s %s", network, address)
		}
		return net.Listen(network, address)
	}
	rt, err := FromConfig(logger.NewLogger(nil, false), "test", characterizedConfig(t, p), components)
	require.Error(t, err, "flag sync listens when the endpoint is created")
	require.Nil(t, rt)
	require.Contains(t, err.Error(), "listen refused")
}
