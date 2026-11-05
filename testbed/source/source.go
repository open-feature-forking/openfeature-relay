// Package source is the testbed's sync provider: a fetcher with the scheme "testbed:" that serves the document bytes
// its owner last loaded, versioned by the version the owner gives (UC-022).
//
// The owner holds a Source and calls Load. Load makes the source's fetcher fetch at once, through an internal trigger,
// so the fetch that follows delivers the loaded bytes under exactly the loaded version: FetchResult.Version is the
// owner's version, which lets the owner tell which delivery carries its load. The source has no endpoint of its own and
// reads nothing from disk or network.
//
// The owner loads before the runtime starts. A fetch before the first load fails and the fetching sync provider retries
// it only after its backoff (about 5 s), without reading the trigger, so a first load that comes later is delivered late.
// The owner also waits for the delivery of each load before the next: loads that arrive while a fetch runs are coalesced
// into one fetch, and the intermediate versions are never delivered.
//
// The trigger is the source's default and is created only for a source of this provider (the type "testbed" is a
// registered trigger type, so a distribution lists it, but every other provider's source is refused). It is not a trigger a
// source of another provider can use.
package source

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	gosync "sync"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
)

const (
	// Provider is the provider name of the testbed's sources, and the type of its trigger.
	Provider = "testbed"

	// Prefix is the scheme of the testbed's sources, such as "testbed:allFlags".
	Prefix = "testbed:"
)

// ErrNothingLoaded is returned by a fetch before the owner has loaded a document.
var ErrNothingLoaded = errors.New("the testbed source holds no document yet")

// Source is the document its owner loads, and the factory of the fetcher and the trigger that serve it.
type Source struct {
	mu      gosync.Mutex
	data    []byte
	version string
	loaded  bool
	signals map[int]func()
	nextID  int
}

// New creates a source that holds no document.
func New() *Source { return &Source{} }

// Load replaces the document with data under version and makes the fetchers of this source fetch at once. The
// version identifies the data: a fetcher given it reports the document unchanged. An empty version is refused.
func (s *Source) Load(data []byte, version string) error {
	if version == "" {
		return errors.New("a loaded document needs a version")
	}
	s.mu.Lock()
	s.data, s.version, s.loaded = slices.Clone(data), version, true
	signals := make([]func(), 0, len(s.signals))
	for _, signal := range s.signals {
		signals = append(signals, signal)
	}
	s.mu.Unlock()
	for _, signal := range signals {
		signal()
	}
	return nil
}

// Unload drops the document: a fetch fails until the owner loads again.
func (s *Source) Unload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data, s.version, s.loaded = nil, "", false
}

// Factory is the factory of the testbed's sync provider for this source: its fetcher, and the trigger that makes the
// fetcher fetch when the owner loads.
func (s *Source) Factory() *Factory { return &Factory{source: s} }

// fetch serves the loaded document. It never fails for a loaded document and reports it unchanged for the version
// it was given.
func (s *Source) fetch(ctx context.Context, previous string) (sync.FetchResult, error) {
	if err := ctx.Err(); err != nil {
		return sync.FetchResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		return sync.FetchResult{}, ErrNothingLoaded
	}
	if previous == s.version {
		return sync.FetchResult{Unchanged: true, Version: s.version}, nil
	}
	return sync.FetchResult{Data: slices.Clone(s.data), Version: s.version, Extension: ".json", ContentType: "application/json"}, nil
}

func (s *Source) subscribe(signal func()) (unsubscribe func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signals == nil {
		s.signals = map[int]func(){}
	}
	id := s.nextID
	s.nextID++
	s.signals[id] = signal
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.signals, id)
	}
}

type fetcher struct{ source *Source }

func (f fetcher) Fetch(ctx context.Context, previous string) (sync.FetchResult, error) {
	return f.source.fetch(ctx, previous)
}

// trigger signals each time the owner loads.
type trigger struct{ source *Source }

func (t trigger) Run(ctx context.Context, signal func()) error {
	unsubscribe := t.source.subscribe(signal)
	defer unsubscribe()
	<-ctx.Done()
	return nil
}

// Factory creates the fetcher and the trigger of the source's sync provider.
type Factory struct{ source *Source }

var (
	_ sync.FetcherFactory   = (*Factory)(nil)
	_ sync.TriggerFactory   = (*Factory)(nil)
	_ sync.DefaultTriggerer = (*Factory)(nil)
)

// Type is the provider name and the name of the trigger.
func (*Factory) Type() string { return Provider }

// Schemes is "testbed:".
func (*Factory) Schemes() []string { return []string{Prefix} }

// ParseURI claims the URIs that start with "testbed:" and keeps them as they are.
func (*Factory) ParseURI(uri string) (sync.SourceConfig, bool) {
	if strings.HasPrefix(uri, Prefix) {
		return sync.SourceConfig{URI: uri, Provider: Provider}, true
	}
	return sync.SourceConfig{}, false
}

// CreateFetcher creates the fetcher of a source.
func (f *Factory) CreateFetcher(settings sync.Settings) (sync.Fetcher, error) {
	if settings.Source.Provider != Provider {
		return nil, fmt.Errorf("source %q: the testbed fetcher serves testbed sources only", settings.Source.URI)
	}
	return fetcher{f.source}, nil
}

// DefaultTriggers is the owner's load, and nothing else: no poller is attached to a testbed source.
func (*Factory) DefaultTriggers(sync.SourceConfig) []sync.TriggerConfig {
	return []sync.TriggerConfig{{Type: Provider}}
}

// CreateTrigger creates the trigger of a testbed source. A source of another provider cannot use it.
func (f *Factory) CreateTrigger(settings sync.Settings) (sync.Trigger, error) {
	if settings.Source.Provider != Provider {
		return nil, fmt.Errorf("source %q: the %s trigger belongs to the testbed source and cannot be selected", settings.Source.URI, Provider)
	}
	return trigger{f.source}, nil
}
