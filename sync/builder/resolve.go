package builder

import (
	"fmt"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/polling"
)

// Resolution is what a source resolves to: the factory of its provider and, for a fetcher, the fetcher with the fetch
// triggers attached to the source.
type Resolution struct {
	// Factory is the factory of the source's provider.
	Factory sync.Factory
	// Source is the source's configuration, its URI rewritten the way the factory's ParseURI does.
	Source sync.SourceConfig
	// Fetcher and Triggers are set for a fetcher; Interval is the polling interval when the poller is attached.
	Fetcher  sync.Fetcher
	Triggers []sync.TriggerConfig
	Interval uint32
	// Streaming is the provider of a streaming sync provider's source.
	Streaming sync.StreamingSyncProvider
}

// Resolve finds the factory of a source's provider, by its provider name, and creates what the provider is made of.
func (sb *SyncBuilder) Resolve(source sync.SourceConfig, log *logger.Logger) (Resolution, error) {
	for _, f := range sb.factories {
		fetcherFactory, isFetcher := f.(sync.FetcherFactory)
		if isFetcher {
			if triggers, ok := sb.fetcherTriggers(fetcherFactory, source); ok {
				if len(source.Triggers) > 0 {
					triggers = source.Triggers
				}
				return sb.resolveFetcher(fetcherFactory, source, triggers, log)
			}
		}
		if streamingFactory, ok := f.(sync.StreamingFactory); ok && f.Type() == source.Provider {
			if len(source.Triggers) > 0 {
				return Resolution{}, fmt.Errorf("the %s sync provider streams its source and takes no fetch triggers", source.Provider)
			}
			provider, err := streamingFactory.CreateStreaming(sync.Settings{
				Source: source, Logger: log, Metrics: sb.SyncMetricsRecorder,
			})
			if err != nil {
				return Resolution{}, err
			}
			return Resolution{Factory: f, Source: source, Streaming: provider}, nil
		}
	}
	return Resolution{}, fmt.Errorf("invalid sync provider: %s, must be one of with %s",
		source.Provider, quoteAnd(sb.providerNames()))
}

// fetcherTriggers reports whether the source's provider name selects the fetcher factory, and the fetch triggers it
// attaches: the one an alias names, else the factory's, else the core's poller.
func (sb *SyncBuilder) fetcherTriggers(f sync.FetcherFactory, source sync.SourceConfig) ([]sync.TriggerConfig, bool) {
	if aliased, ok := f.(sync.Aliased); ok {
		for _, alias := range aliased.Aliases() {
			if alias.Name == source.Provider {
				return []sync.TriggerConfig{alias.Trigger}, true
			}
		}
	}
	if f.Type() != source.Provider {
		return nil, false
	}
	if defaults, ok := f.(sync.DefaultTriggerer); ok {
		if triggers := defaults.DefaultTriggers(source); len(triggers) > 0 {
			return triggers, true
		}
	}
	return []sync.TriggerConfig{{Type: polling.TriggerType}}, true
}

func (sb *SyncBuilder) resolveFetcher(
	f sync.FetcherFactory, source sync.SourceConfig, triggers []sync.TriggerConfig, log *logger.Logger,
) (Resolution, error) {
	for _, trigger := range triggers {
		if err := sb.checkTriggerType(trigger.Type); err != nil {
			return Resolution{}, err
		}
	}
	// the factory's rewriting of the URI, such as stripping "file:", is how the source is read and named
	if parsed, claimed := f.ParseURI(source.URI); claimed {
		source.URI = parsed.URI
	}
	fetcher, err := f.CreateFetcher(sync.Settings{Source: source, Logger: log, Metrics: sb.SyncMetricsRecorder})
	if err != nil {
		return Resolution{}, err
	}

	resolution := Resolution{Factory: f, Source: source, Fetcher: fetcher, Triggers: triggers}
	for _, trigger := range triggers {
		if trigger.Type != polling.TriggerType {
			continue
		}
		interval, err := polling.ResolveInterval(source.Interval)
		if err != nil {
			return Resolution{}, fmt.Errorf("invalid %s sync configuration: %w", source.Provider, err)
		}
		resolution.Interval = interval
	}
	return resolution, nil
}
