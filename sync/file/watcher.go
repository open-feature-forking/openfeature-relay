package file

import (
	"time"

	"github.com/fsnotify/fsnotify"
)

// The names of the file watch triggers, which are also provider names.
const (
	FSNOTIFY = "fsnotify"
	FILEINFO = "fileinfo"
)

const (
	// bounded re-add attempts after a remove event; 6 x 200ms backoff = ~1s window to survive a non-atomic replace (delete then recreate)
	reAddMaxAttempts = 6
	// delay between re-add attempts
	reAddBackoff = 200 * time.Millisecond
)

// Watcher watches a file for changes.
type Watcher interface {
	Close() error
	Add(name string) error
	Remove(name string) error
	WatchList() []string
	Events() chan fsnotify.Event
	Errors() chan error
}
