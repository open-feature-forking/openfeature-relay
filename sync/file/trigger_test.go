package file

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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

// TestClosedWatcherStopsTheProvider pins that a file watch that closes its channels stops a fetching sync provider
// that is set to exit on a trigger error, as a closed watcher stopped the file sync provider.
func TestClosedWatcherStopsTheProvider(t *testing.T) {
	tests := []struct {
		name    string
		close   func(w *closableWatcher)
		wantErr string
	}{
		{"a closed event channel", func(w *closableWatcher) { close(w.events) }, "filepath notifier closed"},
		{"a closed error channel", func(w *closableWatcher) { close(w.errors) }, "watcher error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "flags.json")
			replaceFile(t, path, flagsA, 0o600)
			log := logger.NewLogger(nil, false)
			w := newClosableWatcher()
			trigger := &watchTrigger{
				path: path, logger: log,
				newWatcher: func(context.Context) (Watcher, error) { return w, nil },
			}
			provider := fetching.New(fetching.Options{
				Fetcher: NewFetcher(path), Triggers: []sync.Trigger{trigger}, Logger: log, Source: path, SourceType: sync.SourceFile,
				Legacy: fetching.Legacy{ExitOnTriggerError: true},
			})

			deliveries := make(chan sync.DataSync, 4)
			done := make(chan error, 1)
			go func() { done <- provider.Sync(context.Background(), deliveries) }()

			select {
			case <-deliveries:
			case <-time.After(5 * time.Second):
				t.Fatal("no delivery")
			}
			tt.close(w)
			select {
			case err := <-done:
				require.EqualError(t, err, tt.wantErr)
			case <-time.After(5 * time.Second):
				t.Fatal("the provider did not stop")
			}
		})
	}
}

func TestClosedWatcherIsRunAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flags.json")
	replaceFile(t, path, flagsA, 0o600)
	log := logger.NewLogger(nil, false)
	runs := make(chan *closableWatcher, 4)
	trigger := &watchTrigger{
		path: path, logger: log,
		newWatcher: func(context.Context) (Watcher, error) {
			w := newClosableWatcher()
			runs <- w
			return w, nil
		},
	}
	provider := fetching.New(fetching.Options{
		Fetcher: NewFetcher(path), Triggers: []sync.Trigger{trigger}, Logger: log, Source: path, SourceType: sync.SourceFile,
		Interval: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- provider.Sync(ctx, make(chan sync.DataSync, 8)) }()

	first := <-runs
	close(first.events)
	select {
	case <-runs: // the trigger runs again with a new watcher
	case <-time.After(5 * time.Second):
		t.Fatal("the trigger was not run again")
	}
	cancel()
	require.NoError(t, <-done)
}
