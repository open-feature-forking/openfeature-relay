package builder

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

// replaceFile swaps in a new file the way a Kubernetes ConfigMap update does. The modification time moves
// forward on every call, so the polling watcher sees the change even when the content is the same.
func replaceFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()

	tmp := path + ".new"
	require.NoError(t, os.WriteFile(tmp, []byte(content), mode))
	mtime := time.Now().Add(time.Duration(fileGeneration.Add(1)) * time.Minute)
	require.NoError(t, os.Chtimes(tmp, mtime, mtime))
	require.NoError(t, os.Rename(tmp, path))
}

var fileGeneration atomic.Int64

// TestFileDeliveryCharacterization pins what a file source delivers, and when, for both file watchers.
func TestFileDeliveryCharacterization(t *testing.T) {
	for _, provider := range []string{"fsnotify", "fileinfo"} {
		t.Run(provider, func(t *testing.T) {
			t.Run("a missing path fails Init", func(t *testing.T) {
				missing := filepath.Join(t.TempDir(), "missing.json")
				p, _ := buildSource(t, sync.SourceConfig{URI: missing, Provider: provider})
				require.ErrorContains(t, p.Init(context.Background()), "no such file or directory")
			})

			t.Run("unchanged content delivers nothing", func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "flags.json")
				replaceFile(t, path, flagsA, 0o600)
				src := startSource(t, sync.SourceConfig{URI: path, Provider: provider})

				src.requireDelivery(t, sync.DataSync{FlagData: flagsA, Source: path})
				src.requireNoDelivery(t)

				// the file is written again with the content it had
				replaceFile(t, path, flagsA, 0o600)
				src.requireNoDeliveryWithin(t, 2*time.Second)

				replaceFile(t, path, flagsB, 0o600)
				src.requireDelivery(t, sync.DataSync{FlagData: flagsB, Source: path})
				src.requireNoDelivery(t)

				// an empty file delivers an empty flag configuration, once
				replaceFile(t, path, "", 0o600)
				src.requireDelivery(t, sync.DataSync{FlagData: "{}", Source: path})
				replaceFile(t, path, "", 0o600)
				src.requireNoDeliveryWithin(t, 2*time.Second)
			})

			t.Run("content that cannot be decoded keeps the flags in force", func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "flags.yaml")
				replaceFile(t, path, "flags: {}\n", 0o600)
				src := startSource(t, sync.SourceConfig{URI: path, Provider: provider})
				src.requireDelivery(t, sync.DataSync{FlagData: `{"flags":{}}`, Source: path})

				replaceFile(t, path, "flags: [\n", 0o600)
				src.requireNoDelivery(t)

				// the next content that can be decoded is accepted
				replaceFile(t, path, "flags: {a: {}}\n", 0o600)
				src.requireDelivery(t, sync.DataSync{FlagData: `{"flags":{"a":{}}}`, Source: path})
			})

			t.Run("a deleted file delivers an empty flag configuration", func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "flags.json")
				replaceFile(t, path, flagsA, 0o600)
				src := startSource(t, sync.SourceConfig{URI: path, Provider: provider})
				src.requireDelivery(t, sync.DataSync{FlagData: flagsA, Source: path})

				require.NoError(t, os.Remove(path))
				src.requireDelivery(t, sync.DataSync{FlagData: "{}", Source: path})
			})
		})
	}

	t.Run("fsnotify: a file that can no longer be read keeps the flags in force", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("file permissions do not apply to root")
		}
		path := filepath.Join(t.TempDir(), "flags.json")
		replaceFile(t, path, flagsA, 0o600)
		src := startSource(t, sync.SourceConfig{URI: path, Provider: "fsnotify"})
		src.requireDelivery(t, sync.DataSync{FlagData: flagsA, Source: path})

		replaceFile(t, path, flagsB, 0o000)
		// the watch is added again for about a second before the file is given up
		src.requireNoDeliveryWithin(t, 2*time.Second)
	})
}

// TestFileDeliveriesCarryIncrementalUpdates: a source that opted into incremental updates delivers them.
func TestFileDeliveriesCarryIncrementalUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flags.json")
	replaceFile(t, path, flagsA, 0o600)
	src := startSource(t, sync.SourceConfig{URI: path, Provider: "file", IncrementalUpdates: true})

	src.requireDelivery(t, sync.DataSync{FlagData: flagsA, Source: path, IncrementalUpdates: true})
}
