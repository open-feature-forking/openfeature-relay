package builder

import (
	"context"
	"strings"
	gosync "sync"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

// exampleSource is the in-memory source of the example fetcher.
type exampleSource struct {
	mu      gosync.Mutex
	version int
	flags   string
}

func (e *exampleSource) set(flags string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.version++
	e.flags = flags
}

type exampleFetcher struct{ src *exampleSource }

func (f exampleFetcher) Fetch(_ context.Context, previous string) (sync.FetchResult, error) {
	f.src.mu.Lock()
	defer f.src.mu.Unlock()
	version := strings.Repeat("v", f.src.version)
	if previous == version {
		return sync.FetchResult{Unchanged: true}, nil
	}
	return sync.FetchResult{Data: []byte(f.src.flags), Extension: ".json", Version: version}, nil
}

// exampleFactory is a fetcher factory of an extension, with a scheme no official component has.
type exampleFactory struct{ src *exampleSource }

func (exampleFactory) Type() string      { return "example" }
func (exampleFactory) Schemes() []string { return []string{"example://"} }
func (exampleFactory) ParseURI(uri string) (sync.SourceConfig, bool) {
	if strings.HasPrefix(uri, "example://") {
		return sync.SourceConfig{URI: uri, Provider: "example"}, true
	}
	return sync.SourceConfig{}, false
}

func (f exampleFactory) CreateFetcher(sync.Settings) (sync.Fetcher, error) {
	return exampleFetcher{f.src}, nil
}

// TestExtensionSchemeSelected: a source of an extension's scheme is selected like an official one, by --uri and by
// --sources, and without `triggers` it is refetched on its interval by the core's poller.
func TestExtensionSchemeSelected(t *testing.T) {
	src := &exampleSource{}
	src.set(flagsA)
	factories := append(Official(), exampleFactory{src})

	t.Run("--uri", func(t *testing.T) {
		got, err := ParseSyncProviderURIs([]string{"example://flags", "file:/tmp/f.json"}, factories...)
		require.NoError(t, err)
		require.Equal(t, []sync.SourceConfig{
			{URI: "example://flags", Provider: "example"},
			{URI: "/tmp/f.json", Provider: "file"},
		}, got)

		_, err = ParseSyncProviderURIs([]string{"example://flags"})
		require.ErrorContains(t, err, "invalid sync uri argument: example://flags")
	})

	t.Run("--sources", func(t *testing.T) {
		res, err := NewSyncBuilder(factories...).Resolve(
			sync.SourceConfig{URI: "example://flags", Provider: "example", Interval: 7}, loggerForTests())
		require.NoError(t, err)
		require.Equal(t, "example", res.Factory.Type())
		require.Equal(t, []sync.TriggerConfig{{Type: "poller"}}, res.Triggers)
		require.Equal(t, uint32(7), res.Interval)
	})

	t.Run("an unknown provider name lists the extension too", func(t *testing.T) {
		_, err := NewSyncBuilder(factories...).SyncsFromConfig(
			[]sync.SourceConfig{{URI: "x", Provider: "nope"}}, loggerForTests())
		require.EqualError(t, err, "could not create sync provider: invalid sync provider: nope, must be one of with "+
			"'file', 'fsnotify', 'fileinfo', 'kubernetes', 'http', 'grpc', 'gcs', 'azblob', 's3' or 'example'")
	})

	t.Run("a source without triggers is refetched on its interval", func(t *testing.T) {
		started := startSource(t,
			sync.SourceConfig{URI: "example://flags", Provider: "example", Interval: 1},
			func(sb *SyncBuilder) { sb.factories = factories })
		started.requireDelivery(t, sync.DataSync{FlagData: flagsA, Source: "example://flags"})

		src.set(flagsB)
		started.requireDelivery(t, sync.DataSync{FlagData: flagsB, Source: "example://flags"})
	})
}
