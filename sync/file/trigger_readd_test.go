package file

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

// reAddMockWatcher is a Watcher whose Add fails the first addFailUntil calls, then succeeds
type reAddMockWatcher struct {
	addFailUntil int
	addErr       error
	addCalls     int
	watchList    []string
}

func (m *reAddMockWatcher) Close() error { return nil }

func (m *reAddMockWatcher) Add(name string) error {
	m.addCalls++
	if m.addCalls <= m.addFailUntil {
		return m.addErr
	}
	m.watchList = append(m.watchList, name)
	return nil
}

func (m *reAddMockWatcher) Remove(string) error         { return nil }
func (m *reAddMockWatcher) WatchList() []string         { return m.watchList }
func (m *reAddMockWatcher) Events() chan fsnotify.Event { return make(chan fsnotify.Event) }
func (m *reAddMockWatcher) Errors() chan error          { return make(chan error) }

func newReAddTrigger() *watchTrigger {
	return &watchTrigger{path: "/tmp/does-not-matter/flags.json", logger: logger.NewLogger(nil, false)}
}

// Happy path: file briefly absent (Add fails once) then restored -> watch re-established
func TestReAdd_RetriesUntilSuccess(t *testing.T) {
	mw := &reAddMockWatcher{addFailUntil: 1, addErr: errors.New("no such file or directory")}
	trigger := newReAddTrigger()

	require.NoError(t, trigger.reAdd(context.Background(), mw))
	require.Equal(t, 2, mw.addCalls, "1 fail + 1 success")
	require.Equal(t, []string{trigger.path}, mw.watchList)
}

// Edge case: file truly deleted -> every Add fails, retry gives up and returns the last error
func TestReAdd_ExhaustsAttempts(t *testing.T) {
	wantErr := errors.New("no such file or directory")
	mw := &reAddMockWatcher{addFailUntil: reAddMaxAttempts + 1, addErr: wantErr}

	err := newReAddTrigger().reAdd(context.Background(), mw)
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, reAddMaxAttempts, mw.addCalls)
}

// Cancellation: a cancelled context aborts the retry promptly
func TestReAdd_ContextCancelled(t *testing.T) {
	mw := &reAddMockWatcher{addFailUntil: reAddMaxAttempts + 1, addErr: errors.New("no such file or directory")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the first backoff completes

	start := time.Now()
	err := newReAddTrigger().reAdd(ctx, mw)

	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, mw.addCalls, reAddMaxAttempts)
	require.Less(t, time.Since(start), reAddMaxAttempts*reAddBackoff)
}

func TestSettings(t *testing.T) {
	require.True(t, boolSetting(map[string]any{SettingSignalOnChmod: true}, SettingSignalOnChmod))
	require.False(t, boolSetting(nil, SettingSignalOnChmod))
	require.Equal(t, 250, intSetting(map[string]any{SettingPollIntervalMs: 250}, SettingPollIntervalMs))
	require.Equal(t, 250, intSetting(map[string]any{SettingPollIntervalMs: float64(250)}, SettingPollIntervalMs), "as JSON numbers decode")
	require.Zero(t, intSetting(nil, SettingPollIntervalMs))
}

func TestTriggerFactoryNeedsAPath(t *testing.T) {
	for _, f := range []sync.TriggerFactory{FSNotifyTriggerFactory(), FileInfoTriggerFactory()} {
		_, err := f.CreateTrigger(sync.Settings{Logger: logger.NewLogger(nil, false)})
		require.EqualError(t, err, "no filepath string set", f.Type())
	}
}
