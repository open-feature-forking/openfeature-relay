package file

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
	"github.com/stretchr/testify/require"
)

const (
	flagsA = `{"flags":{"a":{}}}`
	flagsB = `{"flags":{"b":{}}}`
)

var generation atomic.Int64

// replaceFile swaps in a new file the way a Kubernetes ConfigMap update does. The modification time moves forward on
// every call, so the polling watcher sees a change even when the content is the same.
func replaceFile(t testing.TB, path, content string, mode os.FileMode) {
	t.Helper()

	tmp := path + ".new"
	require.NoError(t, os.WriteFile(tmp, []byte(content), mode))
	mtime := time.Now().Add(time.Duration(generation.Add(1)) * time.Minute)
	require.NoError(t, os.Chtimes(tmp, mtime, mtime))
	require.NoError(t, os.Rename(tmp, path))
}

func TestFetcherContract(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not apply to root")
	}
	path := filepath.Join(t.TempDir(), "flags.json")
	replaceFile(t, path, flagsA, 0o600)

	synctest.RunFetcher(t, Factory{}, synctest.FetcherInputs{
		Source: sync.SourceConfig{URI: path, Provider: Provider},
		Change: func(t testing.TB) { replaceFile(t, path, flagsB, 0o600) },
		Break:  func(t testing.TB) { require.NoError(t, os.Chmod(path, 0o000)) },
	})
}

func TestFetcher(t *testing.T) {
	dir := t.TempDir()

	t.Run("Init fails for a path that does not exist", func(t *testing.T) {
		err := NewFetcher(filepath.Join(dir, "missing.json")).Init(context.Background())
		require.ErrorContains(t, err, "no such file or directory")
	})

	t.Run("a file holds its content and a digest as version", func(t *testing.T) {
		path := filepath.Join(dir, "flags.yaml")
		require.NoError(t, os.WriteFile(path, []byte("flags: {}\n"), 0o600))
		f := NewFetcher(path)
		require.NoError(t, f.Init(context.Background()))

		res, err := f.Fetch(context.Background(), "")
		require.NoError(t, err)
		require.Equal(t, "flags: {}\n", string(res.Data))
		require.Equal(t, ".yaml", res.Extension)
		require.NotEmpty(t, res.Version)

		again, err := f.Fetch(context.Background(), res.Version)
		require.NoError(t, err)
		require.True(t, again.Unchanged, "the same content is unchanged")
	})

	t.Run("a file that has vanished, and an empty one, hold no flags", func(t *testing.T) {
		path := filepath.Join(dir, "gone.json")
		require.NoError(t, os.WriteFile(path, nil, 0o600))
		empty, err := NewFetcher(path).Fetch(context.Background(), "")
		require.NoError(t, err)
		require.Empty(t, empty.Data)

		require.NoError(t, os.Remove(path))
		gone, err := NewFetcher(path).Fetch(context.Background(), "")
		require.NoError(t, err)
		require.Empty(t, gone.Data)
		require.Equal(t, empty.Version, gone.Version)
	})

	t.Run("a file that cannot be read is a read error", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("file permissions do not apply to root")
		}
		path := filepath.Join(dir, "locked.json")
		require.NoError(t, os.WriteFile(path, []byte(flagsA), 0o000))
		_, err := NewFetcher(path).Fetch(context.Background(), "")
		require.ErrorIs(t, err, sync.ErrRead)
		require.ErrorIs(t, err, os.ErrPermission)
	})
}

func TestFactory(t *testing.T) {
	f := Factory{}
	require.Equal(t, "file", f.Type())
	require.Equal(t, "file", f.SourceType())
	require.Equal(t, []string{"file:"}, f.Schemes())

	got, ok := f.ParseURI("file:/etc/flags.json")
	require.True(t, ok)
	require.Equal(t, sync.SourceConfig{URI: "/etc/flags.json", Provider: "file"}, got)
	_, ok = f.ParseURI("http://host/flags")
	require.False(t, ok)

	require.Equal(t, map[string]sync.TriggerConfig{"fsnotify": {Type: "fsnotify"}, "fileinfo": {Type: "fileinfo"}}, f.Aliases())

	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	require.Equal(t, []sync.TriggerConfig{{Type: "fileinfo"}}, f.DefaultTriggers(sync.SourceConfig{}))
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	require.Equal(t, []sync.TriggerConfig{{Type: "fsnotify"}}, f.DefaultTriggers(sync.SourceConfig{}))
}

func TestNewSyncProvider(t *testing.T) {
	for _, watchType := range []string{"", FSNOTIFY, FILEINFO} {
		t.Run("watch type "+watchType, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "flags.json")
			replaceFile(t, path, flagsA, 0o600)
			provider, err := NewSyncProvider(path, watchType, logger.NewLogger(nil, false), nil)
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.NoError(t, provider.Init(ctx))
			deliveries := make(chan sync.DataSync, 4)
			done := make(chan error, 1)
			go func() { done <- provider.Sync(ctx, deliveries) }()

			next := func() sync.DataSync {
				select {
				case d := <-deliveries:
					return d
				case <-time.After(8 * time.Second):
					t.Fatal("no delivery")
					return sync.DataSync{}
				}
			}
			require.Equal(t, sync.DataSync{FlagData: flagsA, Source: path}, next())
			replaceFile(t, path, flagsB, 0o600)
			require.Equal(t, sync.DataSync{FlagData: flagsB, Source: path}, next())
			require.Eventually(t, provider.IsReady, time.Second, time.Millisecond)

			cancel()
			require.NoError(t, <-done)
		})
	}

	t.Run("an unknown watch type", func(t *testing.T) {
		_, err := NewSyncProvider("/tmp/x", "inotify", logger.NewLogger(nil, false), nil)
		require.EqualError(t, err, "unknown watcher type: 'inotify'")
	})
}
