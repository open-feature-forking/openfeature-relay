package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	msync "sync"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"go.uber.org/zap"
)

// initChan is the channel Initialized returns; it is created on first use.
func (r *Runtime) initChan() chan struct{} {
	r.initChOnce.Do(func() { r.initializedCh = make(chan struct{}) })
	return r.initializedCh
}

// Initialized returns a channel that is closed once every configured source has delivered a flag configuration
// that a format accepted.
func (r *Runtime) Initialized() <-chan struct{} { return r.initChan() }

// publishChange publishes the configuration change event of a document a format accepted: when it gained,
// changed or lost a flag, or has a version it did not have before. A redelivery of the same document publishes
// nothing. It is called with the runtime's lock held and never blocks.
func (r *Runtime) publishChange(format, source string, d configformat.DocumentReport) {
	if r.Bus == nil {
		return
	}
	if r.versions == nil {
		r.versions = map[string]string{}
	}
	key := format + "\x00" + source + "\x00" + d.FlagSet
	previous, seen := r.versions[key]
	r.versions[key] = d.Version
	if seen && previous == d.Version && len(d.Added)+len(d.Updated)+len(d.Removed) == 0 {
		return
	}
	events.PublishConfigurationChange(r.Bus, events.ConfigurationChange{
		Time: time.Now(), Source: source, Format: format, FlagSet: d.FlagSet, Version: d.Version,
		Added: slices.Clone(d.Added), Updated: slices.Clone(d.Updated), Removed: slices.Clone(d.Removed),
	})
}

// telemetryReserve is the part of the hook budget the event sinks' flush leaves to the telemetry provider's
// shutdown, which runs after it.
const telemetryReserve = time.Second

// namedPipeline is the pipeline of an event sink with the type of the factory that created it.
type namedPipeline struct {
	sinkType string
	pipeline *eventsink.Pipeline
}

// startEventSinks creates the event sinks of the components and subscribes each one to the event bus. A faulty
// declaration or configuration is an error that names the event sink; the sinks started so far are closed again.
func startEventSinks(
	logger *logger.Logger, bus *events.Bus, tp telemetry.Provider, configured []eventsink.Configured,
) (started []namedPipeline, err error) {
	defer func() {
		if err == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		for _, s := range started {
			_ = s.pipeline.Close(ctx)
		}
		started = nil
	}()
	for i, c := range configured {
		if err := eventsink.ValidateDeclarations(c.Factory); err != nil {
			return started, fmt.Errorf("the event sink factory at position %d declares: %w", i, err)
		}
		p, err := eventsink.NewPipeline(bus, c.Factory, eventsink.Settings{
			Logger:    logger.WithFields(zap.String("component", "eventsink"), zap.String("eventsink", c.Factory.Type())),
			Telemetry: tp,
			Config:    c.Config,
		})
		if err != nil {
			return started, err // NewPipeline names the event sink
		}
		started = append(started, namedPipeline{sinkType: c.Factory.Type(), pipeline: p})
	}
	return started, nil
}

// flushEventSinks returns the shutdown hook that delivers what the event sinks still hold. It stops
// telemetryReserve before the hook budget ends, so the telemetry provider's shutdown, which runs next, keeps that
// long; whatever is not delivered by then is counted as dropped with the reason shutdown.
func flushEventSinks(sinks []namedPipeline) func(context.Context) error {
	return func(ctx context.Context) error {
		if deadline, ok := ctx.Deadline(); ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, deadline.Add(-telemetryReserve))
			defer cancel()
		}
		errs := make([]error, len(sinks))
		var wg msync.WaitGroup
		for i, s := range sinks {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := s.pipeline.Close(ctx); err != nil {
					errs[i] = fmt.Errorf("event sink %q: %w", s.sinkType, err)
				}
			}()
		}
		wg.Wait()
		return errors.Join(errs...)
	}
}

// NamedEndpoint is a service endpoint with the type of the factory that created it.
type NamedEndpoint struct {
	Type     string
	Endpoint service.Endpoint
}

// hookBudget is how long the shutdown hooks share once the endpoints and the event bus are done.
const hookBudget = 5 * time.Second

type shutdownHook struct {
	name string
	run  func(context.Context) error
}

// onShutdown registers a hook that runs after the endpoints have stopped and the event bus is closed, in the
// order of registration, with the remaining time of the hook budget. A hook's error is logged.
func (r *Runtime) onShutdown(name string, run func(context.Context) error) {
	r.hooks = append(r.hooks, shutdownHook{name: name, run: run})
}

func (r *Runtime) runShutdownHooks() {
	ctx, cancel := context.WithTimeout(context.Background(), hookBudget)
	defer cancel()
	for _, hook := range r.hooks {
		if err := hook.run(ctx); err != nil {
			r.Logger.Error(fmt.Sprintf("shutdown hook %q: %v", hook.name, err))
		}
	}
}
