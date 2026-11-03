// Package blobfetch is what the fetchers of the blob stores (Google Cloud Storage, Azure Blob Storage, Amazon S3)
// have in common: how a source URI names a bucket and an object, how the object's identity and content make up the
// source version, and the factory around a cloud's store. A cloud's module supplies only the Store, on its own SDK.
package blobfetch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/bloburi"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/utils"
	"go.uber.org/zap"
)

// Store is one object of a blob store.
type Store interface {
	// Identity says which version of the object the store holds now: its ETag or generation, or when it has none,
	// the time it was modified.
	Identity(ctx context.Context) (etag string, modified time.Time, err error)
	// Read returns the object's content and, when the store knows it, its content type.
	Read(ctx context.Context) (data []byte, contentType string, err error)
}

// Fetcher reads a flag configuration from an object of a blob store. Its source version holds what identifies the
// object, its ETag or else its modification time, and the digest of its content: an object whose identity moved
// while its content did not is unchanged.
type Fetcher struct {
	bucket string
	object string
	store  Store
	logger *logger.Logger
}

var (
	_ sync.Fetcher             = (*Fetcher)(nil)
	_ sync.InitializingFetcher = (*Fetcher)(nil)
)

// New creates the fetcher of the object named by a bucket URL and an object name, which bloburi.Split makes of a
// source URI. kind names the cloud in logs. A nil store fails Init, and so does a source without bucket or object.
func New(kind, bucket, object string, store Store, log *logger.Logger) *Fetcher {
	return &Fetcher{
		bucket: bucket,
		object: object,
		store:  store,
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

// splitVersion reads the source version, "<identity> <digest>".
func splitVersion(version string) (identity, digest string) {
	identity, digest, _ = strings.Cut(version, " ")
	return identity, digest
}

// Fetch asks the store which version of the object it holds, then reads the object unless that is the version of the
// previous fetch.
func (f *Fetcher) Fetch(ctx context.Context, previous string) (sync.FetchResult, error) {
	etag, modified, err := f.store.Identity(ctx)
	if err != nil {
		return sync.FetchResult{}, fmt.Errorf("couldn't get object attributes: error fetching attributes for object %s/%s: %w",
			f.bucket, f.object, err)
	}
	identity := etag
	if identity == "" {
		identity = modTimePrefix + strconv.FormatInt(modified.UnixNano(), 10)
	}
	previousIdentity, previousDigest := splitVersion(previous)
	if previous != "" && identity == previousIdentity {
		f.logger.Debug("configuration hasn't changed, skipping fetching full object")
		return sync.FetchResult{Unchanged: true}, nil
	}

	data, contentType, err := f.store.Read(ctx)
	if err != nil {
		return sync.FetchResult{}, fmt.Errorf("couldn't get object: error downloading object %s/%s: %w", f.bucket, f.object, err)
	}

	digest := utils.GenerateSha(data)
	version := identity + " " + digest
	if previous != "" && digest == previousDigest {
		f.logger.Debug("configuration hasn't changed, skipping publishing")
		return sync.FetchResult{Unchanged: true, Version: version}, nil
	}
	return sync.FetchResult{
		Data: data, Extension: filepath.Ext(f.object), ContentType: contentType, Version: version,
	}, nil
}

// Factory creates the fetchers of the blob sources of one cloud.
type Factory struct {
	// Kind is the provider name, such as "gcs".
	Kind string
	// Prefix is the scheme of the cloud's sources, such as "gs://".
	Prefix string
	// Scheme matches through the first "/" after the scheme, such as "^gs://.+?/".
	Scheme *regexp.Regexp
	// Open validates what the cloud needs for a source, such as the options in its bucket URL, and returns the
	// store of its object. It is not called for a source without a bucket, which the fetcher's Init rejects.
	Open func(bucket, object string) (Store, error)
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

// CreateFetcher creates the fetcher of a source. Bucket and object are those of bloburi.Split.
func (f Factory) CreateFetcher(settings sync.Settings) (sync.Fetcher, error) {
	bucket, object := bloburi.Split(settings.Source.URI, f.Scheme)
	var store Store
	if bucket != "" {
		var err error
		if store, err = f.Open(bucket, object); err != nil {
			return nil, err
		}
	}
	return New(f.Kind, bucket, object, store, settings.Logger), nil
}
