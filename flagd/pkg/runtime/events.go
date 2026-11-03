package runtime

import (
	"slices"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
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
