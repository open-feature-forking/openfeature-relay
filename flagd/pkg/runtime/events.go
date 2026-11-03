package runtime

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
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
