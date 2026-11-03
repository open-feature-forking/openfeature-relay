package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/fetching"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/polling"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/utils"
	"github.com/open-feature-forking/openfeature-relay/sync/blob/bloburi"
	"go.uber.org/zap"
	"gocloud.dev/blob"
)

// Fetcher reads a flag configuration from an object of a blob store. Its source version holds what identifies the
// object, its ETag or else its modification time, and the digest of its content: an object whose identity moved
// while its content did not is unchanged.
type Fetcher struct {
	bucket string
	object string
	mux    *blob.URLMux
	logger *logger.Logger
}

var (
	_ sync.Fetcher             = (*Fetcher)(nil)
	_ sync.InitializingFetcher = (*Fetcher)(nil)
)

// NewFetcher creates the fetcher of a blob source. kind names the cloud ("gcs", "azblob", "s3") in logs, and scheme
// matches through the first "/" after the scheme of the source URI ("^s3://.+?/"). The mux must have the cloud's
// driver registered; each cloud package passes the default mux after registering its own driver.
func NewFetcher(kind string, scheme *regexp.Regexp, uri string, log *logger.Logger, mux *blob.URLMux) *Fetcher {
	// Extract bucket uri and object name from the full URI:
	// <scheme>://bucket/path/to/object results in <scheme>://bucket/ as bucketUri and path/to/object as an object
	// name. Any query string is moved onto the bucket URL so the driver reads its options from there.
	bucket, object := bloburi.Split(uri, scheme)
	return &Fetcher{
		bucket: bucket,
		object: object,
		mux:    mux,
		logger: log.WithFields(zap.String("component", "sync"), zap.String("sync", kind)),
	}
}

// Bucket is the URL of the bucket, with the options of the source's URI.
func (f *Fetcher) Bucket() string { return f.bucket }

// Object is the name of the object in the bucket.
func (f *Fetcher) Object() string { return f.object }

// Init checks that the source names a bucket and an object.
func (f *Fetcher) Init(context.Context) error {
	if f.bucket == "" {
		return errors.New("no bucket string set")
	}
	if f.object == "" {
		return errors.New("no object string set")
	}
	return nil
}

const modTimePrefix = "mod:"

// joinVersion and splitVersion build and read the source version: "<identity> <digest>".
func joinVersion(identity, digest string) string { return identity + " " + digest }

func splitVersion(version string) (identity, digest string) {
	identity, digest, _ = strings.Cut(version, " ")
	return identity, digest
}

// Fetch reads the object's attributes, then the object unless its identity is the one of the previous version.
func (f *Fetcher) Fetch(ctx context.Context, previous string) (sync.FetchResult, error) {
	bucket, err := f.mux.OpenBucket(ctx, f.bucket)
	if err != nil {
		return sync.FetchResult{}, fmt.Errorf("couldn't get bucket: error opening bucket %s: %v", f.bucket, err)
	}
	defer bucket.Close()

	attrs, err := bucket.Attributes(ctx, f.object)
	if err != nil {
		return sync.FetchResult{}, fmt.Errorf("couldn't get object attributes: error fetching attributes for object %s/%s: %w",
			f.bucket, f.object, err)
	}
	identity := attrs.ETag
	if identity == "" {
		identity = modTimePrefix + strconv.FormatInt(attrs.ModTime.UnixNano(), 10)
	}
	previousIdentity, previousDigest := splitVersion(previous)
	if previous != "" && identity == previousIdentity {
		f.logger.Debug("configuration hasn't changed, skipping fetching full object")
		return sync.FetchResult{Unchanged: true}, nil
	}

	r, err := bucket.NewReader(ctx, f.object, nil)
	if err != nil {
		return sync.FetchResult{}, fmt.Errorf("couldn't get object: error opening reader for object %s/%s: %w", f.bucket, f.object, err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return sync.FetchResult{}, fmt.Errorf("couldn't get object: error downloading object %s/%s: %w", f.bucket, f.object, err)
	}

	digest := utils.GenerateSha(data)
	version := joinVersion(identity, digest)
	if previous != "" && digest == previousDigest {
		f.logger.Debug("configuration hasn't changed, skipping publishing")
		return sync.FetchResult{Unchanged: true, Version: version}, nil
	}
	return sync.FetchResult{
		Data: data, Extension: filepath.Ext(f.object), ContentType: r.ContentType(), Version: version,
	}, nil
}

// NewSync builds the sync provider of a blob source from its configuration: the blob fetcher, polled on the
// source's interval. It keeps the behaviour the blob sync provider had: a failed first fetch stops it, fetches have
// no deadline, and deliveries are never incremental updates.
func NewSync(
	kind string, scheme *regexp.Regexp, config sync.SourceConfig, log *logger.Logger,
	recorder sync.MetricsRecorder, mux *blob.URLMux,
) (sync.StreamingSyncProvider, error) {
	interval, err := polling.ResolveInterval(config.Interval)
	if err != nil {
		return nil, fmt.Errorf("invalid %s sync configuration: %w", kind, err)
	}
	trigger, err := polling.NewIntervalTrigger(interval, config.IntervalSeed)
	if err != nil {
		return nil, fmt.Errorf("invalid %s sync configuration: %w", kind, err)
	}
	return fetching.New(fetching.Options{
		Fetcher:            NewFetcher(kind, scheme, config.URI, log, mux),
		Triggers:           []sync.Trigger{trigger},
		Logger:             log,
		Source:             config.URI,
		SourceType:         sync.SourceBlob,
		Metrics:            recorder,
		IncrementalUpdates: config.IncrementalUpdates,
		Legacy:             fetching.Legacy{NoDeadline: true, IgnoreIncremental: true},
	}), nil
}

// Factory creates the fetchers of the blob sources of one cloud.
type Factory struct {
	// Kind is the provider name, such as "gcs".
	Kind string
	// Prefix is the scheme of the cloud's sources, such as "gs://".
	Prefix string
	// Scheme matches through the first "/" after the scheme, such as "^gs://.+?/".
	Scheme *regexp.Regexp
	// Mux has the cloud's driver registered.
	Mux *blob.URLMux
	// Check validates what the cloud's sources need from the environment; nil needs nothing.
	Check func() error
}

var (
	_ sync.FetcherFactory = Factory{}
	_ sync.SourceTyper    = Factory{}
)

// Type is the provider name of the cloud.
func (f Factory) Type() string { return f.Kind }

// SourceType is the type the sync metrics record: every cloud is a blob store.
func (f Factory) SourceType() string { return sync.SourceBlob }

// Schemes is the scheme of the cloud's sources.
func (f Factory) Schemes() []string { return []string{f.Prefix} }

// ParseURI claims the cloud's URIs as they are.
func (f Factory) ParseURI(uri string) (sync.SourceConfig, bool) {
	if strings.HasPrefix(uri, f.Prefix) {
		return sync.SourceConfig{URI: uri, Provider: f.Kind}, true
	}
	return sync.SourceConfig{}, false
}

// CreateFetcher creates the fetcher of a source.
func (f Factory) CreateFetcher(settings sync.Settings) (sync.Fetcher, error) {
	if f.Check != nil {
		if err := f.Check(); err != nil {
			return nil, err
		}
	}
	return NewFetcher(f.Kind, f.Scheme, settings.Source.URI, settings.Logger, f.Mux), nil
}

// Mux is the URL mux the fetcher opens its bucket with.
func (f *Fetcher) Mux() *blob.URLMux { return f.mux }
