package builder

import (
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/fetching"
)

// The official fetchers run in the legacy mode of the fetching sync provider: they behave as the sync providers they
// replace did. The behaviour changes of the use case are made one at a time by taking a switch out of this file.

// legacyFor gives the legacy switches of an official fetcher; any other fetcher has the behaviour of the fetching sync
// provider.
func legacyFor(sync.Factory) fetching.Legacy {
	return fetching.Legacy{}
}
