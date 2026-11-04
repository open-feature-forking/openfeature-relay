package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/open-feature-forking/openfeature-relay/runtime"
	runtimecmd "github.com/open-feature-forking/openfeature-relay/runtime/cmd"
	"github.com/open-feature-forking/openfeature-relay/service/transport/gate"
	"github.com/open-feature-forking/openfeature-relay/testbed/control"
	"github.com/open-feature-forking/openfeature-relay/testbed/source"
)

const (
	// combinedFile is the file source of the testbed configurations that holds the combined flags. The testbed serves
	// it from memory instead (Q23).
	combinedFile = "flags/allFlags.json"
	// combinedSource is the name of the source of the combined flags. The URI stays as the configuration writes it,
	// because selectors address a source by its URI; only the provider behind it changes.
	combinedSource = combinedFile
	// pollInterval is how often a start looks at the readiness of the runtime it waits for.
	pollInterval = 10 * time.Millisecond
)

// rewriteCombined serves the file source of the combined flags from the testbed source, keeping its URI, and leaves
// every other source as written.
func rewriteCombined(s coresync.SourceConfig) coresync.SourceConfig {
	if s.Provider == "file" && s.URI == combinedFile {
		s.Provider = source.Provider
	}
	return s
}

// target drives a flagd runtime for the control server. The server calls it from one goroutine at a time.
type target struct {
	configs string
	version string
	// gate is the gate of the runtime in use. Each runtime listens through a gate of its own, so that a build that fails
	// leaves no listener registered for the next one.
	gate   *gate.Gate
	gateMu sync.Mutex
	// onListen, when set, is called before each listener of a runtime is opened.
	onListen   func()
	source     *source.Source
	components distribution.Components
	log        *logger.Logger
	// document returns the combined flag document and its version.
	document func() ([]byte, string)

	loads atomic.Uint64

	mu      sync.Mutex
	rt      *runtime.Runtime
	current string
	cancel  context.CancelFunc
	done    chan error
}

var _ control.Target = (*target)(nil)

func newTarget(configs, version string, src *source.Source, components distribution.Components, log *logger.Logger) *target {
	components.Telemetry = telemetry.NoopFactory()
	return &target{configs: configs, version: version, gate: gate.New(), source: src, components: components, log: log}
}

// currentGate returns the gate of the runtime in use.
func (t *target) currentGate() *gate.Gate {
	t.gateMu.Lock()
	defer t.gateMu.Unlock()
	return t.gate
}

// newGate closes the gate in use and replaces it by a closed one, whose Listen the next runtime's components use.
func (t *target) newGate() *gate.Gate {
	t.gateMu.Lock()
	defer t.gateMu.Unlock()
	t.gate.Close()
	g := gate.New()
	g.Close()
	t.gate = g
	return g
}

// Start closes the gate and brings the runtime of the configuration to a loaded store, ready but closed. The runtime is
// reset when it is the one in use and built again when the configuration changed or the last build failed.
func (t *target) Start(ctx context.Context, config string) error {
	path := filepath.Join(t.configs, config+".json")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w %q", control.ErrUnknownConfiguration, config)
		}
		return err
	}

	t.currentGate().Close()
	t.mu.Lock()
	rt, current := t.rt, t.current
	t.mu.Unlock()
	if rt == nil || current != config {
		if err := t.rebuild(ctx, config, path); err != nil {
			return err
		}
	} else if err := rt.Reset(ctx); err != nil {
		return err
	}
	return nil
}

// Open opens the gate. The server calls it once it has rendered the combined flags.
func (t *target) Open(context.Context) error { return t.currentGate().Open() }

// rebuild stops the runtime in use, waits for it to release its addresses, and runs a new one.
func (t *target) rebuild(ctx context.Context, config, path string) error {
	if err := t.stopRuntime(ctx); err != nil {
		return err
	}

	g := t.newGate()
	listening := t.components
	listening.Listen = func(network, address string) (net.Listener, error) {
		if t.onListen != nil {
			t.onListen()
		}
		return g.Listen(network, address)
	}
	cfg, components, err := runtimecmd.LoadConfig(path, listening, []runtimecmd.SourceRewrite{rewriteCombined})
	if err != nil {
		return err
	}
	rt, err := runtime.FromConfig(t.log, t.version, cfg, components)
	if err != nil {
		return err
	}
	document, version := t.document()
	if err := t.source.Load(document, version); err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- rt.Run(runCtx)
		close(done)
	}()
	t.mu.Lock()
	t.rt, t.cancel, t.done = rt, cancel, done
	t.mu.Unlock()

	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for !rt.Ready() {
		select {
		case err := <-done:
			t.forget()
			if err == nil {
				err = errors.New("the runtime stopped")
			}
			return err
		case <-ctx.Done():
			// the gate is closed, so the runtime stops quickly; a later change must not load into it
			stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = t.stopRuntime(stop)
			return ctx.Err()
		case <-tick.C:
		}
	}
	t.mu.Lock()
	t.current = config
	t.mu.Unlock()
	return nil
}

// stopRuntime cancels the runtime in use and waits for its Run to return, so that its listeners are closed and their
// addresses free before the next runtime listens. It returns when ctx ends too, with the runtime forgotten.
func (t *target) stopRuntime(ctx context.Context) error {
	t.mu.Lock()
	cancel, done := t.cancel, t.done
	t.rt, t.cancel, t.done, t.current = nil, nil, nil, ""
	t.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("the previous runtime did not stop: %w", ctx.Err())
	}
}

// forget drops the runtime after a Run that returned on its own.
func (t *target) forget() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
	t.rt, t.cancel, t.done, t.current = nil, nil, nil, ""
}

// Stop closes the gate. The runtime keeps running behind it.
func (t *target) Stop(context.Context) error {
	t.currentGate().Close()
	return nil
}

// Load hands the runtime the combined document through the testbed source and waits until its store holds that version.
func (t *target) Load(ctx context.Context, document []byte, version string) error {
	t.mu.Lock()
	rt := t.rt
	t.mu.Unlock()
	if rt == nil {
		return control.ErrNoRuntime
	}
	// A toggle back gives a document the runtime has accepted before, and WaitAccepted returns at once for a version it
	// knows, so each load carries a version of its own.
	version = fmt.Sprintf("%s-%d", version, t.loads.Add(1))
	if err := t.source.Load(document, version); err != nil {
		return err
	}
	return rt.WaitAccepted(ctx, combinedSource, version)
}

// Ready reports whether a runtime is running and ready.
func (t *target) Ready() bool {
	t.mu.Lock()
	rt := t.rt
	t.mu.Unlock()
	return rt != nil && rt.Ready()
}

// shutdown stops the runtime when the command ends.
func (t *target) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.currentGate().Close()
	_ = t.stopRuntime(ctx)
}
