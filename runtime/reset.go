package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"golang.org/x/sync/errgroup"
)

// barrierSource is the source of the marker Reset puts on the delivery channel: the delivery loop reaches it only after
// every delivery queued before it.
const barrierSource = "runtime:reset-barrier"

// rejection is the last delivery of a source that its configuration format rejected.
type rejection struct {
	version string
	err     error
}

// Run serves the runtime until ctx ends, which is a normal stop. It is Start for a caller that owns the lifetime:
// it handles no signals, so a program that builds and stops runtimes one after another, such as a test bed, can
// run them in one process.
func (r *Runtime) Run(ctx context.Context) error { return r.start(ctx) }

// Reset empties the store and loads every source again, so that the store holds exactly what the sources hold, as it
// did after the start. It leaves the listeners and the endpoints as they are; a caller that must not serve a store
// that is not loaded yet keeps its own gate shut while Reset runs.
//
// Reset removes every flag and document in one step, takes the runtime out of readiness, forgets the versions it has
// accepted, and has every sync provider deliver its full flag configuration again, a source that has not changed
// included. It returns when every source has been accepted, or when ctx ends, with an error that names the sources
// that were not. Concurrent calls run one after the other. The runtime must be running (Run or Start).
func (r *Runtime) Reset(ctx context.Context) error {
	r.resetMu.Lock()
	defer r.resetMu.Unlock()

	r.mu.Lock()
	switch {
	case r.store == nil:
		r.mu.Unlock()
		return errors.New("this runtime has no store of its own to reset")
	case r.deliveries == nil:
		r.mu.Unlock()
		return errors.New("the runtime is not running")
	}
	r.store.Clear()
	r.initialized.Store(false)
	for source := range r.sourceReadiness {
		r.sourceReadiness[source] = false
	}
	// the reload publishes configuration change events and answers WaitAccepted as a first start does
	clear(r.versions)
	clear(r.accepted)
	clear(r.rejected)
	done := make(chan struct{})
	if len(r.sourceReadiness) == 0 {
		close(done)
	} else {
		r.resetDone = done
	}
	deliveries, stopped := r.deliveries, r.stopped
	r.mu.Unlock()

	// the reload ends with the runtime: nothing consumes the deliveries after that
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-stopped:
			cancel()
		case <-runCtx.Done():
		}
	}()

	g, gCtx := errgroup.WithContext(runCtx)
	for _, provider := range r.Syncs {
		g.Go(func() error {
			if err := provider.ReSync(gCtx, deliveries); err != nil {
				return fmt.Errorf("sync provider ReSync returned error: %w", err)
			}
			return nil
		})
	}
	err := g.Wait()

	// a delivery queued before the wipe may have been accepted after it; once the loop has passed the marker, every
	// delivery queued before the reload has been, and the reload was the last
	if err == nil {
		err = r.awaitBarrier(runCtx, deliveries, stopped)
	}
	if err == nil {
		select {
		case <-done:
			return nil
		case <-stopped:
			err = errRunStopped
		case <-ctx.Done():
			err = ctx.Err()
		}
	} else if errors.Is(err, context.Canceled) && ctx.Err() == nil {
		err = errRunStopped
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resetDone == done {
		r.resetDone = nil
	}
	return fmt.Errorf("sources not accepted again: %s: %w", r.pendingSources(), err)
}

var errRunStopped = errors.New("the runtime stopped during the reset")

// awaitBarrier puts a marker on deliveries and waits until the delivery loop has processed it.
func (r *Runtime) awaitBarrier(ctx context.Context, deliveries chan<- sync.DataSync, stopped <-chan struct{}) error {
	barrier := make(chan struct{})
	r.mu.Lock()
	r.barrier = barrier
	r.mu.Unlock()
	select {
	case deliveries <- sync.DataSync{Source: barrierSource}:
	case <-stopped:
		return errRunStopped
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-barrier:
		return nil
	case <-stopped:
		return errRunStopped
	case <-ctx.Done():
		return ctx.Err()
	}
}

// passBarrier is the delivery loop's answer to the marker.
func (r *Runtime) passBarrier() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.barrier != nil {
		close(r.barrier)
		r.barrier = nil
	}
}

// pendingSources names the sources that have not been accepted since the last reset. The lock is held.
func (r *Runtime) pendingSources() string {
	var pending []string
	for source, ready := range r.sourceReadiness {
		if !ready {
			pending = append(pending, fmt.Sprintf("%q", source))
		}
	}
	sort.Strings(pending)
	return strings.Join(pending, ", ")
}

// WaitAccepted blocks until the last delivery of source that the runtime accepted was fetched at version (the
// version a fetching sync provider reports, DataSync.Version), so the store holds it. A source that goes back to an
// earlier version is waited for again. A delivery that the
// configuration format rejects ends the wait with the format's error. It also ends when ctx does. A reset forgets the
// versions, so the wait is for the reload's delivery.
func (r *Runtime) WaitAccepted(ctx context.Context, source, version string) error {
	if version == "" {
		return errors.New("WaitAccepted needs a source version")
	}
	for {
		r.mu.Lock()
		if r.accepted[source] == version {
			r.mu.Unlock()
			return nil
		}
		if rej, ok := r.rejected[source]; ok && rej.version == version {
			r.mu.Unlock()
			return rej.err
		}
		changed := r.acceptSignal()
		r.mu.Unlock()

		select {
		case <-changed:
		case <-ctx.Done():
			return fmt.Errorf("source %q was not delivered at version %q: %w", source, version, ctx.Err())
		}
	}
}

// acceptSignal returns the channel that is closed at the next acceptance or rejection. The lock is held.
func (r *Runtime) acceptSignal() <-chan struct{} {
	if r.acceptChanged == nil {
		r.acceptChanged = make(chan struct{})
	}
	return r.acceptChanged
}

// noteDelivery records the outcome of a delivery for WaitAccepted. The lock is held.
func (r *Runtime) noteDelivery(payload sync.DataSync, err error) {
	if payload.Version == "" {
		return
	}
	if err != nil {
		if r.rejected == nil {
			r.rejected = map[string]rejection{}
		}
		r.rejected[payload.Source] = rejection{version: payload.Version, err: err}
	} else {
		if r.accepted == nil {
			r.accepted = map[string]string{}
		}
		r.accepted[payload.Source] = payload.Version
		if rej, ok := r.rejected[payload.Source]; ok && rej.version == payload.Version {
			delete(r.rejected, payload.Source)
		}
	}
	if r.acceptChanged != nil {
		close(r.acceptChanged)
		r.acceptChanged = nil
	}
}
