package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/fetching"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/utils"
	"go.uber.org/zap"
)

// Provider is the provider name of file sources.
const Provider = "file"

// Prefix is the prefix of a file source's URI that names the scheme; the path follows it.
const Prefix = "file:"

// Fetcher reads a flag configuration from a file. A file that does not exist, or is empty, holds no flags. Its
// source version is the digest of the content.
type Fetcher struct {
	path string
}

var (
	_ sync.Fetcher             = (*Fetcher)(nil)
	_ sync.InitializingFetcher = (*Fetcher)(nil)
)

// NewFetcher creates the fetcher of the file at path.
func NewFetcher(path string) *Fetcher {
	return &Fetcher{path: path}
}

// Path is the path the fetcher reads.
func (f *Fetcher) Path() string { return f.path }

// Init checks that the path exists: a mistyped path stops the distribution at startup.
func (f *Fetcher) Init(context.Context) error {
	if f.path == "" {
		return errors.New("no filepath string set")
	}
	if _, err := os.Stat(f.path); err != nil {
		return fmt.Errorf("error opening %s: %w", f.path, err)
	}
	return nil
}

// Fetch reads the file. A file that has vanished since startup returns no data. Any other failure to read it is an
// error that wraps sync.ErrRead.
func (f *Fetcher) Fetch(_ context.Context, previous string) (sync.FetchResult, error) {
	data, err := os.ReadFile(f.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return sync.FetchResult{}, fmt.Errorf("error reading file %s: %w", f.path, errors.Join(sync.ErrRead, err))
	}
	// a file that does not exist holds no data, like an empty one
	version := utils.GenerateSha(data)
	if previous == version {
		return sync.FetchResult{Unchanged: true, Version: version}, nil
	}
	return sync.FetchResult{Data: data, Extension: filepath.Ext(f.path), Version: version}, nil
}

// Factory creates the fetchers of file sources. It is also selected by the names of its two fetch triggers.
type Factory struct{}

var (
	_ sync.FetcherFactory   = Factory{}
	_ sync.SourceTyper      = Factory{}
	_ sync.DefaultTriggerer = Factory{}
	_ sync.Aliased          = Factory{}
)

// Type is the provider name.
func (Factory) Type() string { return Provider }

// SourceType is the type the sync metrics record.
func (Factory) SourceType() string { return sync.SourceFile }

// Schemes are the prefixes of file sources.
func (Factory) Schemes() []string { return []string{Prefix} }

// ParseURI claims "file:" URIs and strips the prefix.
func (Factory) ParseURI(uri string) (sync.SourceConfig, bool) {
	if path, ok := strings.CutPrefix(uri, Prefix); ok {
		return sync.SourceConfig{URI: path, Provider: Provider}, true
	}
	return sync.SourceConfig{}, false
}

// CreateFetcher creates the fetcher of a source.
func (Factory) CreateFetcher(settings sync.Settings) (sync.Fetcher, error) {
	return NewFetcher(settings.Source.URI), nil
}

// DefaultTriggers is the file watch: fsnotify inside Kubernetes, where mounted ConfigMaps change through symbolic
// links, and polling the file's status outside.
func (Factory) DefaultTriggers(sync.SourceConfig) []sync.TriggerConfig {
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		return []sync.TriggerConfig{{Type: FILEINFO}}
	}
	return []sync.TriggerConfig{{Type: FSNOTIFY}}
}

// Aliases are the provider names that select the file provider with one specific file watch.
func (Factory) Aliases() map[string]sync.TriggerConfig {
	return map[string]sync.TriggerConfig{
		FSNOTIFY: {Type: FSNOTIFY},
		FILEINFO: {Type: FILEINFO},
	}
}

// NewSyncProvider creates the sync provider of a file source in one call, for embedders: the file fetcher with the
// file watch named by watchType (FSNOTIFY, FILEINFO; empty means FSNOTIFY).
func NewSyncProvider(
	uri, watchType string, log *logger.Logger, recorder sync.MetricsRecorder,
) (sync.StreamingSyncProvider, error) {
	var trigger sync.Trigger
	switch watchType {
	case FSNOTIFY, "":
		trigger = NewFSNotifyTrigger(uri, log, nil)
	case FILEINFO:
		trigger = NewFileInfoTrigger(uri, log, nil)
	default:
		return nil, fmt.Errorf("unknown watcher type: '%s'", watchType)
	}
	return fetching.New(fetching.Options{
		Fetcher:    NewFetcher(uri),
		Triggers:   []sync.Trigger{trigger},
		Logger:     log.WithFields(zap.String("component", "sync")),
		Source:     uri,
		SourceType: sync.SourceFile,
		Metrics:    recorder,
	}), nil
}
