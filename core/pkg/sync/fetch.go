package sync

import (
	"context"
	"errors"
)

// Fetcher reads one source on request. It knows nothing about when to fetch: the core's fetching sync provider
// decides, driven by the fetch triggers attached to the source. A Fetcher is used by one fetching sync provider and
// never concurrently.
//
// A fetcher may also implement InitializingFetcher, called once before the first fetch, and io.Closer, called on
// shutdown.
type Fetcher interface {
	// Fetch reads the source once. previous is the source version of the last fetch the provider accepted, empty for a
	// full fetch. A fetch that fails returns an error and no flag configuration; it never returns a partial result. A
	// fetch abandons its work when ctx is done.
	Fetch(ctx context.Context, previous string) (FetchResult, error)
}

// InitializingFetcher is a Fetcher that needs a step before its first fetch. An error stops the distribution, as an
// error of a sync provider's Init does.
type InitializingFetcher interface {
	Init(ctx context.Context) error
}

// FetchResult is what a fetch of a source returns.
type FetchResult struct {
	// Data is the flag configuration as read, undecoded. Empty data means the source holds no flags: the provider
	// delivers an empty flag configuration for it.
	Data []byte
	// Extension is the file extension of the source, such as ".yaml", when known.
	Extension string
	// ContentType is the media type of Data, when known.
	ContentType string
	// Version is the source version: an opaque value (an ETag, a content hash, a generation) that identifies Data. The
	// provider hands it back as previous on the next fetch.
	Version string
	// Unchanged reports that the source still has the flag configuration of previous. Data is then ignored. A
	// non-empty Version replaces previous for the next fetch, for a source whose validator moved while its content
	// did not.
	Unchanged bool
}

// ErrRead marks a failed fetch that could not read the source's content, as opposed to reaching the source. A fetcher
// wraps its error with it (fmt.Errorf("...: %w", ...) joined with ErrRead).
var ErrRead = errors.New("unable to read the source")

// Trigger tells the fetcher of a source to fetch. It never fetches and never delivers a flag configuration itself.
type Trigger interface {
	// Run calls signal each time the trigger's change signal arrives, until ctx is done. signal never blocks. Run
	// returns nil on shutdown. A returned error is logged and Run is called again after a backoff, while the other
	// triggers of the source keep running; a trigger therefore reconnects to its change signal by itself or returns an
	// error.
	Run(ctx context.Context, signal func()) error
}

// OutcomeObserver is implemented by a Trigger that adapts to how the fetches it caused went, such as the core's
// poller, which backs off. The provider calls Observe once per finished fetch, with its error, nil on success.
type OutcomeObserver interface {
	Observe(err error)
}
