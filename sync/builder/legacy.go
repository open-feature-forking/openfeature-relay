package builder

import (

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
	case file.Provider:
		return fetching.Legacy{CountOnlyRead: true}
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

