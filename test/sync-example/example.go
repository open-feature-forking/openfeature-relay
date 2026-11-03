// Package syncexample is an example sync extension: a fetcher for an `example://` source that reads an in-memory
// flag configuration, and a fetch trigger that signals on SIGHUP. It shows what an Extension Author's component
// module contains, and imports only the core packages a fetcher and a fetch trigger may (no sync provider,
// telemetry or transport).
//
// A distribution registers the factories next to the official ones; a source then names the trigger in its
// `triggers`:
//
//	{"uri": "example://flags", "provider": "example", "triggers": [{"type": "sighup"}]}
package syncexample

import (
	"context"
	"errors"
	"fmt"
	"os"
	ossignal "os/signal"
	"strconv"
	"strings"
	gosync "sync"
	"syscall"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
)

// Type is the provider name by which a source selects the example fetcher.
const Type = "example"

// TriggerType is the type by which a source selects the SIGHUP fetch trigger.
const TriggerType = "sighup"

// Source is the in-memory flag configuration the example fetcher reads.
type Source struct {
	mu      gosync.Mutex
	flags   string
	version int
	failing bool
}

// NewSource creates a source with a flag configuration.
func NewSource(flags string) *Source {
	return &Source{flags: flags, version: 1}
}

// Set replaces the flag configuration.
func (s *Source) Set(flags string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flags = flags
	s.version++
}

// Fail makes every fetch of the source fail, or succeed again.
func (s *Source) Fail(failing bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failing = failing
}

// Fetcher fetches a Source. Its source version is the number of times the source was set.
type Fetcher struct{ source *Source }

var _ sync.Fetcher = Fetcher{}

// Fetch implements sync.Fetcher.
func (f Fetcher) Fetch(ctx context.Context, previous string) (sync.FetchResult, error) {
	if err := ctx.Err(); err != nil {
		return sync.FetchResult{}, err
	}
	f.source.mu.Lock()
	defer f.source.mu.Unlock()
	if f.source.failing {
		return sync.FetchResult{}, errors.New("the example source is unavailable")
	}
	version := strconv.Itoa(f.source.version)
	if previous == version {
		return sync.FetchResult{Unchanged: true, Version: version}, nil
	}
	return sync.FetchResult{Data: []byte(f.source.flags), Extension: ".json", Version: version}, nil
}

// Factory creates the example fetcher; a distribution registers it next to the official factories.
type Factory struct{ Source *Source }

var _ sync.FetcherFactory = Factory{}

// Type implements sync.Factory.
func (Factory) Type() string { return Type }

// Schemes implements sync.SchemeFactory.
func (Factory) Schemes() []string { return []string{"example://"} }

// ParseURI implements sync.SchemeFactory.
func (Factory) ParseURI(uri string) (sync.SourceConfig, bool) {
	if !strings.HasPrefix(uri, "example://") {
		return sync.SourceConfig{}, false
	}
	return sync.SourceConfig{URI: uri, Provider: Type}, true
}

// CreateFetcher implements sync.FetcherFactory.
func (f Factory) CreateFetcher(sync.Settings) (sync.Fetcher, error) {
	if f.Source == nil {
		return nil, fmt.Errorf("the %s fetcher needs a source", Type)
	}
	return Fetcher{f.Source}, nil
}

// TriggerFactory creates the SIGHUP fetch trigger. The trigger has no settings, so a source's `triggers` entry is
// just {"type": "sighup"}.
type TriggerFactory struct {
	// Subscribe delivers the hangup signals until stop is called; nil subscribes to the process's SIGHUP. A closed
	// channel means the subscription was lost.
	Subscribe func() (signals <-chan os.Signal, stop func())
}

var _ sync.TriggerFactory = TriggerFactory{}

// Type implements sync.Factory.
func (TriggerFactory) Type() string { return TriggerType }

// CreateTrigger implements sync.TriggerFactory.
func (f TriggerFactory) CreateTrigger(sync.Settings) (sync.Trigger, error) {
	subscribe := f.Subscribe
	if subscribe == nil {
		subscribe = subscribeToSIGHUP
	}
	return hangupTrigger{subscribe}, nil
}

func subscribeToSIGHUP() (<-chan os.Signal, func()) {
	signals := make(chan os.Signal, 1)
	ossignal.Notify(signals, syscall.SIGHUP)
	return signals, func() { ossignal.Stop(signals) }
}

type hangupTrigger struct {
	subscribe func() (<-chan os.Signal, func())
}

// Run signals each time a hangup arrives. It returns nil when ctx is done, and an error when the subscription is
// lost, in which case the fetching sync provider runs it again.
func (t hangupTrigger) Run(ctx context.Context, signal func()) error {
	signals, stop := t.subscribe()
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-signals:
			if !ok {
				return errors.New("the SIGHUP subscription was lost")
			}
			signal()
		}
	}
}
