//go:build linux

package runtime

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	flagdcomponents "github.com/open-feature/flagd/flagd/components"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// cycles is how many times a test builds or resets a runtime.
const cycles = 200

// readyWithin is the longest a rebuilt or reset runtime may take to be ready.
const readyWithin = time.Second

func openFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)
	return len(entries)
}

// settledFDs is the descriptor count once it has stopped moving. The count is the process's, and what other tests left
// running, such as a client closing a connection or a server finishing a handshake, changes it for a moment.
func settledFDs(t *testing.T) int {
	t.Helper()
	const stableFor = 10
	deadline := time.Now().Add(5 * time.Second)
	last, same := openFDs(t), 0
	for same < stableFor && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		if n := openFDs(t); n == last {
			same++
		} else {
			last, same = n, 0
		}
	}
	return last
}

// requireNoFDLeak fails unless the descriptor count comes back to the baseline within a few seconds. A leak does not
// close, so it fails after the wait; a descriptor of another test that is closing does not.
func requireNoFDLeak(t *testing.T, baseline int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for openFDs(t) > baseline && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	require.LessOrEqual(t, openFDs(t), baseline, "file descriptors leaked")
}

// probe asks the management port whether the runtime is ready, without keeping connections.
type probe struct {
	client *http.Client
	url    string
}

func newProbe(port uint16) probe {
	return probe{
		client: &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: time.Second},
		url:    fmt.Sprintf("http://127.0.0.1:%d/readyz", port),
	}
}

func (p probe) ready() bool {
	resp, err := p.client.Get(p.url) //nolint:noctx // local test endpoint
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// waitReady polls until the runtime is ready and returns how long that took.
func (p probe) waitReady(t *testing.T, since time.Time) time.Duration {
	t.Helper()
	for time.Since(since) < 10*time.Second {
		if p.ready() {
			return time.Since(since)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the runtime did not become ready")
	return 0
}

func twoSourceConfig(t *testing.T, p ports) Config {
	t.Helper()
	cfg := characterizedConfig(t, p)
	second := filepath.Join(t.TempDir(), "second.json")
	require.NoError(t, os.WriteFile(second, []byte(flagsJSON("f2")), 0o600))
	cfg.SyncProviders = append(cfg.SyncProviders, coresync.SourceConfig{URI: second, Provider: "file"})
	return cfg
}

func officialComponents() Components {
	components := flagdcomponents.Components()
	components.Telemetry = nil
	return components
}

// build makes a runtime from the configuration and runs it; the returned function stops it and waits for Run.
func build(t *testing.T, cfg Config) (*Runtime, func()) {
	return buildWith(t, cfg, officialComponents())
}

// buildWith is build for these components.
func buildWith(t *testing.T, cfg Config, components Components) (*Runtime, func()) {
	t.Helper()
	rt, err := FromConfig(logger.NewLogger(nil, false), "test", cfg, listening(components))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()
	return rt, func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(20 * time.Second):
			t.Fatal("Run did not stop")
		}
	}
}

// Building, running and stopping a runtime over and over in one process leaves nothing behind: no goroutine, no file
// descriptor, no port. The testbed switches configurations this way.
func TestFromConfigRepeatedly(t *testing.T) {
	p := newPorts(t)
	cfg := twoSourceConfig(t, p)
	check := newProbe(p.management)

	cycle := func() time.Duration {
		start := time.Now()
		rt, stop := build(t, cfg)
		elapsed := check.waitReady(t, start)
		resolved, _, _, _, err := rt.Evaluator.ResolveBooleanValue(context.Background(), "id", "f1", nil)
		require.NoError(t, err)
		require.True(t, resolved)
		stop()
		return elapsed
	}

	cycle() // the first cycle starts what the libraries start once
	check.client.CloseIdleConnections()
	ignore := goleak.IgnoreCurrent()
	fds := settledFDs(t)

	var slowest time.Duration
	for i := range cycles {
		elapsed := cycle()
		slowest = max(slowest, elapsed)
		require.Less(t, elapsed, readyWithin, "cycle %d took %s to become ready", i, elapsed)
	}
	t.Logf("%d cycles, slowest ready after %s", cycles, slowest)

	check.client.CloseIdleConnections()
	goleak.VerifyNone(t, ignore)
	requireNoFDLeak(t, fds)
}

// Resetting one runtime over and over is as clean, and each reset has the runtime ready again within the second.
func TestResetRepeatedly(t *testing.T) {
	p := newPorts(t)
	rt, stop := build(t, twoSourceConfig(t, p))
	check := newProbe(p.management)
	check.waitReady(t, time.Now())

	ctx := context.Background()
	reset := func() time.Duration {
		start := time.Now()
		resetCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		require.NoError(t, rt.Reset(resetCtx))
		require.True(t, check.ready(), "not ready after Reset returned")
		for _, key := range []string{"f1", "f2"} {
			value, _, _, _, err := rt.Evaluator.ResolveBooleanValue(ctx, "id", key, nil)
			require.NoError(t, err, key)
			require.True(t, value, key)
		}
		return time.Since(start)
	}

	reset()
	check.client.CloseIdleConnections()
	ignore := goleak.IgnoreCurrent()
	fds := settledFDs(t)

	var slowest time.Duration
	for i := range cycles {
		elapsed := reset()
		slowest = max(slowest, elapsed)
		require.Less(t, elapsed, readyWithin, "reset %d took %s", i, elapsed)
	}
	t.Logf("%d resets, slowest %s", cycles, slowest)

	check.client.CloseIdleConnections()
	requireNoFDLeak(t, fds)
	goleak.VerifyNone(t, ignore)
	stop()
}

// A request that arrives while the store is reset gets a flag or FLAG_NOT_FOUND, never another error and never a
// value that is not the flag's.
func TestResetUnderLoad(t *testing.T) {
	p := newPorts(t)
	rt, stop := build(t, twoSourceConfig(t, p))
	defer stop()
	newProbe(p.management).waitReady(t, time.Now())

	loadCtx, stopLoad := context.WithCancel(context.Background())
	type outcome struct {
		found, notFound int
		bad             []string
	}
	results := make(chan outcome, 4)
	for range cap(results) {
		go func() {
			var o outcome
			for loadCtx.Err() == nil {
				for _, key := range []string{"f1", "f2"} {
					value, _, _, _, err := rt.Evaluator.ResolveBooleanValue(context.Background(), "id", key, nil)
					switch {
					case err == nil && value:
						o.found++
					case err != nil && err.Error() == model.FlagNotFoundErrorCode:
						o.notFound++
					default:
						o.bad = append(o.bad, fmt.Sprintf("%s: value %v, error %v", key, value, err))
					}
				}
			}
			results <- o
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for range 50 {
		require.NoError(t, rt.Reset(ctx))
	}
	stopLoad()

	var found, notFound int
	for range cap(results) {
		o := <-results
		require.Empty(t, o.bad, "answers that are neither the flag nor FLAG_NOT_FOUND")
		found += o.found
		notFound += o.notFound
	}
	require.Positive(t, found, "the readers saw the flags")
	t.Logf("%d answers with the flag, %d FLAG_NOT_FOUND during resets", found, notFound)
}
