package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	msync "sync"
	"sync/atomic"
	"syscall"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation/ofrep"
	flagsync "github.com/open-feature/flagd/flagd/pkg/service/flag-sync"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

type Runtime struct {
	Evaluator         evaluator.IEvaluator
	Logger            *logger.Logger
	SyncService       flagsync.ISyncService
	OfrepService      ofrep.IOfrepService
	EvaluationService service.IFlagEvaluationService //nolint:staticcheck // the runtime serves endpoints instead from the runtime change on
	ServiceConfig     service.Configuration
	Syncs             []sync.ISync
	// Formats holds the configuration format of each source that names one other than flagd's, keyed
	// by source. Sources without an entry update the Evaluator.
	Formats map[string]configformat.ConfigurationFormat
	// DefaultFormat is the configuration format of the sources that name none, flagd's own. FromConfig sets it,
	// and every source then goes through a format. Without it those sources update the Evaluator, publish no
	// configuration change events and keep the log text of the evaluator.
	DefaultFormat configformat.ConfigurationFormat
	// Bus carries the configuration change events the runtime publishes once a format accepts a delivery.
	Bus *events.Bus

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

	mu msync.Mutex
}

func (r *Runtime) Start() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return r.start(ctx)
}

//nolint:funlen
func (r *Runtime) start(ctx context.Context) error {
	if r.EvaluationService == nil {
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
		r.EvaluationService.Shutdown()
		r.Logger.Info("Server successfully shutdown.")
	}()

	g.Go(func() error {
		// Readiness probe rely on the runtime
		r.ServiceConfig.ReadinessProbe = r.isReady
		if err := r.EvaluationService.Serve(gCtx, r.ServiceConfig); err != nil {
			return fmt.Errorf("error returned from serving flag evaluation service: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		err := r.OfrepService.Start(gCtx)
		if err != nil {
			return fmt.Errorf("error from ofrep server: %w", err)
		}

		return nil
	})

	g.Go(func() error {
		err := r.SyncService.Start(gCtx)
		if err != nil {
			return fmt.Errorf("error from sync server: %w", err)
		}

		return nil
	})

	if err := g.Wait(); err != nil {
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
		return true
	}
	return false
}

// updateAndEmit helps to update state, notify changes and trigger sync updates
func (r *Runtime) updateAndEmit(payload sync.DataSync) {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, span := otel.Tracer("jsonEvaluator").Start(
		context.Background(),
		"flagSync",
		trace.WithAttributes(attribute.String("feature_flag.source", payload.Source)))
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
		span.SetStatus(codes.Error, "flagSync error")
		span.RecordError(err)
	}
	span.End()
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
		}
	}
	r.SyncService.Emit(payload.Source)
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
