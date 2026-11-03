package builder

import (
	"context"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/fetching"
	"github.com/open-feature-forking/openfeature-relay/sync/azblob"
	"github.com/open-feature-forking/openfeature-relay/sync/file"
	"github.com/open-feature-forking/openfeature-relay/sync/gcs"
	httpSync "github.com/open-feature-forking/openfeature-relay/sync/http"
	"github.com/open-feature-forking/openfeature-relay/sync/s3"
)

// The official fetchers run in the legacy mode of the fetching sync provider: they behave as the sync providers they
// replace did. The behaviour changes of the use case are made one at a time by taking a switch out of this file.

// legacyFor gives the legacy switches of an official fetcher; any other fetcher has the behaviour of the fetching sync
// provider.
func legacyFor(f sync.Factory) fetching.Legacy {
	switch f.Type() {
	case httpSync.Provider, gcs.Provider, azblob.Provider, s3.Provider:
		return fetching.Legacy{NoDeadline: true, IgnoreIncremental: true}
	case file.Provider:
		return fetching.Legacy{
			RedeliverUnchanged: true, NoDeadline: true, IgnoreIncremental: true,
			CountOnlyRead: true, ExitOnTriggerError: true,
		}
	}
	return fetching.Legacy{}
}

// legacyPollerWithoutBackoff reports whether the poller of an official fetcher stays on its schedule while fetches
// fail, as the pollers of the providers it replaces did.
func legacyPollerWithoutBackoff(f sync.Factory) bool {
	switch f.Type() {
	case httpSync.Provider, gcs.Provider, azblob.Provider, s3.Provider:
		return true
	}
	return false
}

// fullFetches is a fetcher that is always asked for the full flag configuration. The file sync provider delivered the
// file again on every change of it, whatever its content.
type fullFetches struct{ sync.Fetcher }

func (f fullFetches) Fetch(ctx context.Context, _ string) (sync.FetchResult, error) {
	return f.Fetcher.Fetch(ctx, "")
}

func (f fullFetches) Init(ctx context.Context) error {
	if init, ok := f.Fetcher.(sync.InitializingFetcher); ok {
		return init.Init(ctx)
	}
	return nil
}

// legacyFetcher wraps the fetcher of an official provider where the legacy mode needs it.
func legacyFetcher(f sync.Factory, fetcher sync.Fetcher) sync.Fetcher {
	if f.Type() == file.Provider {
		return fullFetches{fetcher}
	}
	return fetcher
}
