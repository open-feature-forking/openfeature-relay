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

// acceptedHistory is how many accepted versions of a source WaitAccepted remembers.
const acceptedHistory = 32

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
	deliveries := r.deliveries
	r.mu.Unlock()

	g, gCtx := errgroup.WithContext(ctx)
	for _, provider := range r.Syncs {
		g.Go(func() error {
			if err := provider.ReSync(gCtx, deliveries); err != nil {
				return fmt.Errorf("sync provider ReSync returned error: %w", err)
			}
			return nil
		})
	}
	err := g.Wait()

	if err == nil {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resetDone == done {
		r.resetDone = nil
	}
	return fmt.Errorf("sources not accepted again: %s: %w", r.pendingSources(), err)
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

// WaitAccepted blocks until the runtime has accepted the delivery of source that was fetched at version (the
// version a fetching sync provider reports, DataSync.Version), and the store holds it. A delivery that the
// configuration format rejects ends the wait with the format's error. It also ends when ctx does. A reset forgets the
// versions, so the wait is for the reload's delivery.
func (r *Runtime) WaitAccepted(ctx context.Context, source, version string) error {
	if version == "" {
		return errors.New("WaitAccepted needs a source version")
	}
	for {
		r.mu.Lock()
		for _, v := range r.accepted[source] {
			if v == version {
				r.mu.Unlock()
				return nil
			}
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
			r.accepted = map[string][]string{}
		}
		versions := r.accepted[payload.Source]
		for i, v := range versions {
			if v == payload.Version {
				versions = append(versions[:i], versions[i+1:]...)
				break
			}
		}
		versions = append(versions, payload.Version)
		if len(versions) > acceptedHistory {
			versions = versions[len(versions)-acceptedHistory:]
		}
		r.accepted[payload.Source] = versions
		if rej, ok := r.rejected[payload.Source]; ok && rej.version == payload.Version {
			delete(r.rejected, payload.Source)
		}
	}
	if r.acceptChanged != nil {
		close(r.acceptChanged)
		r.acceptChanged = nil
	}
}
