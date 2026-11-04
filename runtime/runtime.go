package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	msync "sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"golang.org/x/sync/errgroup"
)

// staleLimit is the age of the last contact with a source beyond which flagd is not ready.
type staleLimit struct {
	source   string
	provider sync.ContactReporter
	after    time.Duration
}

type Runtime struct {
	Evaluator     evaluator.IEvaluator
	Logger        *logger.Logger
	ServiceConfig service.Configuration
	Syncs         []sync.ISync
	// Formats holds the configuration format of each source that names one other than flagd's, keyed
	// by source. Sources without an entry update the Evaluator.
	Formats map[string]configformat.ConfigurationFormat
	// DefaultFormat is the configuration format of the sources that name none, flagd's own. FromConfig sets it,
	// and every source then goes through a format. Without it those sources update the Evaluator, publish no
	// configuration change events and keep the log text of the evaluator.
	DefaultFormat configformat.ConfigurationFormat
	// Bus carries the configuration change events the runtime publishes once a format accepts a delivery.
	Bus *events.Bus
	// Endpoints are the service endpoints the runtime serves, created by FromConfig from the distribution's factories.
	Endpoints []NamedEndpoint
	hooks     []shutdownHook
	// MetricsHandler serves /metrics on the management port; nil answers 404.
	MetricsHandler http.Handler
	// Listen opens the management port; nil means net.Listen.
	Listen service.ListenFunc
	// Telemetry is the telemetry provider the runtime records its flagSync spans through; nil means the noop
	// telemetry provider.
	Telemetry telemetry.Provider
	// shuttingDown makes the readiness probe fail once the runtime stops.
	shuttingDown atomic.Bool

	// versions holds the version last accepted for each document, to tell a new version from a redelivery.
	versions map[string]string
	// initializedCh is closed once every configured source has delivered and been accepted.
	initializedCh   chan struct{}
	initializedOnce msync.Once
	initChOnce      msync.Once

	// sourceReadiness records whether each configured source has successfully updated the evaluator.
	sourceReadiness map[string]bool
	// initialized is set once every configured source has successfully updated the evaluator.
	initialized atomic.Bool
	// staleLimits are the sources that go stale, and how soon.
	staleLimits []staleLimit
	// now is the clock of the staleness check; nil means time.Now.
	now func() time.Time

	// store is the store FromConfig built, which Reset empties; nil for a runtime assembled by hand.
	store *store.Store
	// deliveries is the channel the sync providers deliver on while the runtime runs; Reset hands it to ReSync.
	deliveries chan sync.DataSync
	// resetDone is closed once every source has been accepted again after a Reset; nil outside one.
	resetDone chan struct{}
	// resetMu serializes Reset calls.
	resetMu msync.Mutex
	// accepted holds, per source, the most recent versions of the deliveries a format accepted; rejected the last one
	// it rejected. WaitAccepted reads them, and acceptChanged wakes it.
	accepted      map[string][]string
	rejected      map[string]rejection
	acceptChanged chan struct{}

	mu msync.Mutex
}

func (r *Runtime) Start() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return r.start(ctx)
}

//nolint:funlen
func (r *Runtime) start(ctx context.Context) error {
	if len(r.Endpoints) == 0 {
		return errors.New("no service set")
	}
	if len(r.Syncs) == 0 {
		return errors.New("no sync implementation set")
	}
	if r.Evaluator == nil && r.DefaultFormat == nil {
		return errors.New("no evaluator set")
	}
	g, gCtx := errgroup.WithContext(ctx)
	dataSync := make(chan sync.DataSync, len(r.Syncs))
	r.mu.Lock()
	r.deliveries = dataSync
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.deliveries = nil
		r.mu.Unlock()
	}()
	// Initialize DataSync channel watcher
	g.Go(func() error {
		for {
			select {
			case data := <-dataSync:
				r.updateAndEmit(data)
			case <-gCtx.Done():
				return nil
			}
		}
	})
	// Init sync providers
	for _, s := range r.Syncs {
		if err := s.Init(gCtx); err != nil {
			return fmt.Errorf("sync provider Init returned error: %w", err)
		}
	}
	// Start sync provider
	for _, s := range r.Syncs {
		p := s
		g.Go(func() error {
			if err := p.Sync(gCtx, dataSync); err != nil {
				return fmt.Errorf("sync provider returned error: %w", err)
			}
			return nil
		})
	}

	defer func() {
		r.Logger.Info("Shutting down server...")
		r.Logger.Info("Server successfully shutdown.")
	}()

	g.Go(func() error {
		if err := r.serveManagement(gCtx); err != nil {
			return fmt.Errorf("error from the management port: %w", err)
		}
		return nil
	})

	for _, named := range r.Endpoints {
		g.Go(func() error {
			if err := named.Endpoint.Serve(gCtx); err != nil {
				return fmt.Errorf("error from service endpoint %q: %w", named.Type, err)
			}
			return nil
		})
	}

	err := g.Wait()
	// the endpoints are done: later events are dropped, and the shutdown hooks run in the order they were registered
	if r.Bus != nil {
		r.Bus.Close()
	}
	r.runShutdownHooks()
	if err != nil {
		return fmt.Errorf("errgroup closed with error: %w", err)
	}
	return nil
}

func (r *Runtime) isReady() bool {
	if r.initialized.Load() {
		// if all providers can watch for flag changes, we are ready.
		for _, p := range r.Syncs {
			if !p.IsReady() {
				return false
			}
		}
		return !r.anyStale()
	}
	return false
}

// updateAndEmit helps to update state, notify changes and trigger sync updates
func (r *Runtime) updateAndEmit(payload sync.DataSync) {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, span := telemetry.OrNoop(r.Telemetry).Tracer("jsonEvaluator").Start(
		context.Background(),
		"flagSync",
		telemetry.WithAttributes(telemetry.String("feature_flag.source", payload.Source)))
	var err error
	if format, ok := r.Formats[payload.Source]; ok {
		err = r.accept(format, payload)
	} else if r.DefaultFormat != nil {
		err = r.accept(r.DefaultFormat, payload)
		// flagd's log text stays what the evaluator wrote; the span and the debug line name the source
		var rejection configformat.Rejection
		if errors.As(err, &rejection) {
			err = errors.New(rejection.Reason)
		}
	} else {
		err = r.Evaluator.SetState(payload)
	}
	if err != nil {
		span.SetError("flagSync error")
		span.RecordError(err)
	}
	span.End()
	r.noteDelivery(payload, err)
	if err != nil {
		r.Logger.Error(fmt.Sprintf("error setting state: %v", err))
		return
	}

	if !r.initialized.Load() {
		if _, configured := r.sourceReadiness[payload.Source]; configured {
			r.sourceReadiness[payload.Source] = true
		}

		allReady := true
		for _, ready := range r.sourceReadiness {
			allReady = allReady && ready
		}
		if allReady {
			r.initialized.Store(true)
			r.initializedOnce.Do(func() { close(r.initChan()) })
			if r.resetDone != nil {
				close(r.resetDone)
				r.resetDone = nil
			}
		}
	}
}

// accept hands a payload to the configuration format of its source. A rejection names source and entry.
func (r *Runtime) accept(format configformat.ConfigurationFormat, payload sync.DataSync) error {
	result, err := format.Accept(context.Background(), configformat.Input{
		Source: payload.Source, Data: payload.FlagData, Selector: payload.Selector,
		IncrementalUpdate: payload.IncrementalUpdates,
	})
	if err != nil {
		return err
	}
	for _, d := range result.Documents {
		r.Logger.Debug(fmt.Sprintf(
			"source %q, format %q, flag set %q, version %s: %d flags, %d added, %d updated, %d removed",
			payload.Source, format.Type(), d.FlagSet, d.Version, len(d.FlagKeys), len(d.Added), len(d.Updated), len(d.Removed)))
		r.publishChange(format.Type(), payload.Source, d)
	}
	for _, dropped := range result.Dropped {
		r.Logger.Warn(fmt.Sprintf("dropped: %v", dropped))
	}
	return nil
}

// anyStale reports whether a source with a staleAfter setting has not been in contact within it. A source that has
// never been in contact is stale.
func (r *Runtime) anyStale() bool {
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	for _, limit := range r.staleLimits {
		last := limit.provider.LastContact()
		if last.IsZero() || now().Sub(last) > limit.after {
			return true
		}
	}
	return false
}
