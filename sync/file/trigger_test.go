package file

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/fetching"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
	"github.com/stretchr/testify/require"
)

func TestTriggerContract(t *testing.T) {
	factories := []sync.TriggerFactory{FSNotifyTriggerFactory(), FileInfoTriggerFactory()}
	for _, f := range factories {
		t.Run(f.Type(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "flags.json")
			replaceFile(t, path, flagsA, 0o600)
			change := func(t testing.TB) { replaceFile(t, path, flagsB, 0o600) }

			synctest.RunTrigger(t, f, synctest.TriggerInputs{
				Source:  sync.SourceConfig{URI: path},
				Signal:  change,
				Timeout: 8 * time.Second,
				// a ConfigMap update removes the file the watch is on: the trigger has to watch the new file
				Interrupt: change,
			})
		})
	}
}

// TestClosedWatcherDoesNotStopTheProvider pins that a file watch that closes its channels does not stop the fetching
// sync provider, and is run again with a new watcher.
func TestClosedWatcherDoesNotStopTheProvider(t *testing.T) {
	tests := []struct {
		name  string
		close func(w *closableWatcher)
	}{
		{"a closed event channel", func(w *closableWatcher) { close(w.events) }},
		{"a closed error channel", func(w *closableWatcher) { close(w.errors) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "flags.json")
			replaceFile(t, path, flagsA, 0o600)
			log := logger.NewLogger(nil, false)
			watchers := make(chan *closableWatcher, 4)
			// the watcher that replaces the closed one is created only when the test lets it, so that the file can change
			// in between
			reconnect := make(chan struct{})
			var created atomic.Int32
			trigger := &watchTrigger{
				path: path, logger: log,
				newWatcher: func(context.Context) (Watcher, error) {
					if created.Add(1) > 1 {
						<-reconnect
					}
					w := newClosableWatcher()
					watchers <- w
					return w, nil
				},
			}
			provider := fetching.New(fetching.Options{
				Fetcher: NewFetcher(path), Triggers: []sync.Trigger{trigger}, Logger: log, Source: path, SourceType: sync.SourceFile,
				Interval: 5 * time.Millisecond,
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			deliveries := make(chan sync.DataSync, 4)
			done := make(chan error, 1)
			go func() { done <- provider.Sync(ctx, deliveries) }()

			select {
			case <-deliveries:
			case <-time.After(5 * time.Second):
				t.Fatal("no delivery")
			}
			tt.close(<-watchers)
			replaceFile(t, path, flagsB, 0o600)
			close(reconnect)
			select {
			case <-watchers: // the trigger runs again with a new watcher
			case err := <-done:
				t.Fatalf("the provider stopped with %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("the trigger was not run again")
			}
			select {
			case got := <-deliveries:
				require.Equal(t, flagsB, got.FlagData, "the change made while the watch was lost is picked up")
			case <-time.After(5 * time.Second):
				t.Fatal("the change made while the watch was lost was not delivered")
			}
			cancel()
			require.NoError(t, <-done)
		})
	}
}

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
