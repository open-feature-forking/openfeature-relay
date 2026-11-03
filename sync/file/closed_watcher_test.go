package file

import (
	"context"
	msync "sync"
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

// closableWatcher is a Watcher whose channels the test closes.
type closableWatcher struct {
	events chan fsnotify.Event
	errors chan error
}

func newClosableWatcher() *closableWatcher {
	return &closableWatcher{events: make(chan fsnotify.Event), errors: make(chan error)}
}

func (w *closableWatcher) Close() error                { return nil }
func (w *closableWatcher) Add(string) error            { return nil }
func (w *closableWatcher) Remove(string) error         { return nil }
func (w *closableWatcher) WatchList() []string         { return nil }
func (w *closableWatcher) Events() chan fsnotify.Event { return w.events }
func (w *closableWatcher) Errors() chan error          { return w.errors }

// TestClosedWatcherCharacterization pins that a watcher that closes its channels ends Sync with an error,
// which stops the flagd runtime.
func TestClosedWatcherCharacterization(t *testing.T) {
	newSync := func(w Watcher) *Sync {
		return &Sync{URI: "/nonexistent/flags.json", Logger: logger.NewLogger(nil, false), watcher: w, Mux: &msync.RWMutex{}}
	}

	t.Run("a closed event channel", func(t *testing.T) {
		w := newClosableWatcher()
		s := newSync(w)
		close(w.events)

		err := s.Sync(context.Background(), make(chan sync.DataSync, 1))
		require.EqualError(t, err, "filepath notifier closed")
	})

	t.Run("a closed error channel", func(t *testing.T) {
		w := newClosableWatcher()
		s := newSync(w)
		close(w.errors)

		err := s.Sync(context.Background(), make(chan sync.DataSync, 1))
		require.EqualError(t, err, "watcher error")
		require.False(t, s.IsReady())
	})
}
