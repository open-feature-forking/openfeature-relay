package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	gosync "sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"go.uber.org/zap"
)

// Settings of the file watch triggers, in a source's `triggers` entry.
const (
	// SettingSignalOnChmod makes the trigger signal when the file's permissions change, so that a fixed permission
	// is picked up.
	SettingSignalOnChmod = "signalOnChmod"
	// SettingPollIntervalMs is the polling interval, in milliseconds, of the fileinfo trigger.
	SettingPollIntervalMs = "pollIntervalMs"
)

// watchTrigger signals when the file it watches changes.
type watchTrigger struct {
	path          string
	logger        *logger.Logger
	newWatcher    func(ctx context.Context) (Watcher, error)
	signalOnChmod bool

	mu      gosync.Mutex
	watcher Watcher // established by Init, taken by Run
}

var _ sync.Trigger = (*watchTrigger)(nil)

// NewFSNotifyTrigger creates a trigger that watches the file with fsnotify.
func NewFSNotifyTrigger(path string, log *logger.Logger, settings map[string]any) sync.Trigger {
	return &watchTrigger{
		path:   path,
		logger: log.WithFields(zap.String("component", "sync"), zap.String("sync", FSNOTIFY)),
		newWatcher: func(context.Context) (Watcher, error) {
			w, err := NewFSNotifyWatcher()
			if err != nil {
				return nil, fmt.Errorf("error creating fsnotify watcher: %w", err)
			}
			return w, nil
		},
		signalOnChmod: boolSetting(settings, SettingSignalOnChmod),
	}
}

// NewFileInfoTrigger creates a trigger that watches the file by polling its status.
func NewFileInfoTrigger(path string, log *logger.Logger, settings map[string]any) sync.Trigger {
	log = log.WithFields(zap.String("component", "sync"), zap.String("sync", FILEINFO))
	pollIntervalMs := intSetting(settings, SettingPollIntervalMs)
	return &watchTrigger{
		path:   path,
		logger: log,
		newWatcher: func(ctx context.Context) (Watcher, error) {
			return NewFileInfoWatcher(ctx, pollIntervalMs, log), nil
		},
		signalOnChmod: boolSetting(settings, SettingSignalOnChmod),
	}
}

func boolSetting(settings map[string]any, key string) bool {
	v, _ := settings[key].(bool)
	return v
}

func intSetting(settings map[string]any, key string) int {
	switch v := settings[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}

// Init establishes the watch, so that a change from now on is not missed.
func (w *watchTrigger) Init(ctx context.Context) error {
	watcher, err := w.openWatcher(ctx)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.watcher = watcher
	w.mu.Unlock()
	return nil
}

func (w *watchTrigger) openWatcher(ctx context.Context) (Watcher, error) {
	watcher, err := w.newWatcher(ctx)
	if err != nil {
		return nil, err
	}
	if err := watcher.Add(w.path); err != nil {
		_ = watcher.Close()
		return nil, fmt.Errorf("error adding watcher %s: %w", w.path, err)
	}
	return watcher, nil
}

// Run watches the file until ctx is done, on the watch Init established or else on a new one. It returns an error
// when the watcher closes its channels.
func (w *watchTrigger) Run(ctx context.Context, signal func()) error {
	w.mu.Lock()
	watcher := w.watcher
	w.watcher = nil
	w.mu.Unlock()
	if watcher == nil {
		var err error
		if watcher, err = w.openWatcher(ctx); err != nil {
			return err
		}
	}
	defer watcher.Close()

	w.logger.Debug(fmt.Sprintf("watching %s for changes", w.path))
	for {
		select {
		case event, ok := <-watcher.Events():
			if !ok {
				w.logger.Info("filepath notifier closed")
				return errors.New("filepath notifier closed")
			}
			w.logger.Info(fmt.Sprintf("filepath event: %s %s", event.Name, event.Op.String()))
			w.handle(ctx, watcher, event, signal)

		case err, ok := <-watcher.Errors():
			if !ok {
				return errors.New("watcher error")
			}
			w.logger.Error(err.Error())

		case <-ctx.Done():
			w.logger.Debug("exiting file watcher")
			return nil
		}
	}
}

func (w *watchTrigger) handle(ctx context.Context, watcher Watcher, event fsnotify.Event, signal func()) {
	switch {
	case event.Has(fsnotify.Create) || event.Has(fsnotify.Write):
		signal()
	case event.Has(fsnotify.Remove):
		// K8s exposes config maps as symlinks, and an update removes the old one: add the watch again. A non-atomic
		// replace (delete then recreate) leaves the file briefly absent, so retry before concluding it is gone.
		if err := w.reAdd(ctx, watcher); err != nil {
			w.logger.Error(fmt.Sprintf("error restoring watcher, file may have been deleted: %s", err.Error()))
		}
		// At the point the remove event is fired the new data is in place, so signal either way.
		signal()
	case event.Has(fsnotify.Chmod):
		// on linux the REMOVE event will not fire until all file descriptors are closed, which cannot happen
		// while the file is being watched, so os.Stat is used to infer deletion
		if _, err := os.Stat(w.path); w.signalOnChmod || errors.Is(err, os.ErrNotExist) {
			signal()
		}
	}
}

// reAdd adds the watch again, retrying with a backoff; it returns the last error, or ctx.Err() if cancelled.
func (w *watchTrigger) reAdd(ctx context.Context, watcher Watcher) error {
	var err error
	for attempt := 0; attempt < reAddMaxAttempts; attempt++ {
		if err = watcher.Add(w.path); err == nil {
			return nil
		}
		if attempt == reAddMaxAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(reAddBackoff):
		}
	}
	return err
}

// triggerFactory creates the file watch trigger of one name.
type triggerFactory struct {
	name   string
	create func(path string, log *logger.Logger, settings map[string]any) sync.Trigger
}

// FSNotifyTriggerFactory creates the fsnotify trigger.
func FSNotifyTriggerFactory() sync.TriggerFactory {
	return triggerFactory{name: FSNOTIFY, create: NewFSNotifyTrigger}
}

// FileInfoTriggerFactory creates the fileinfo trigger.
func FileInfoTriggerFactory() sync.TriggerFactory {
	return triggerFactory{name: FILEINFO, create: NewFileInfoTrigger}
}

func (f triggerFactory) Type() string { return f.name }

// CreateTrigger watches the source's path.
func (f triggerFactory) CreateTrigger(settings sync.Settings) (sync.Trigger, error) {
	if settings.Source.URI == "" {
		return nil, errors.New("no filepath string set")
	}
	return f.create(settings.Source.URI, settings.Logger, settings.Config), nil
}
