// Package gcs is the sync provider for flag configurations in Google Cloud Storage buckets: a fetcher on the Cloud
// Storage client library.
//
// A source is "gs://bucket/path/to/object", with these options in its query:
//
//   - anonymous=true reads without credentials;
//   - access_id=- does the same, and any other access_id is accepted and unused (it only matters for signing URLs);
//   - private_key_path=<file> must name a readable file, and is unused for the same reason.
//
// Without options the client uses Application Default Credentials. STORAGE_EMULATOR_HOST points it at an emulator.
package gcs

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	gosync "sync"
	"time"

	"cloud.google.com/go/storage"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/blobfetch"
	"google.golang.org/api/option"
)

// Provider is the name by which a source selects this sync provider.
const Provider = "gcs"

// scheme matches through the first "/" after the scheme, e.g. "gs://bucket/".
var scheme = regexp.MustCompile("^gs://.+?/")

// Matches reports whether uri is a gs:// URI.
func Matches(uri string) bool {
	return scheme.MatchString(uri)
}

// NewFactory creates the factory of gs:// sources.
func NewFactory() blobfetch.Factory {
	return factory(nil)
}

// factory creates the factory with extra options for the storage client, which the tests use to reach a stub.
func factory(clientOptions []option.ClientOption) blobfetch.Factory {
	return blobfetch.Factory{
		Kind: Provider, Prefix: "gs://", Scheme: scheme,
		Open: func(bucketURL, object string) (blobfetch.Store, error) {
			return open(bucketURL, object, clientOptions)
		},
	}
}

// store is an object of a bucket. The generation it reads is the one its last Identity call saw, so that what is read
// is what was compared.
type store struct {
	bucket        string
	object        string
	anonymous     bool
	clientOptions []option.ClientOption

	mu         gosync.Mutex
	generation int64
}

// open reads the options of a bucket URL, as "gs://bucket?anonymous=true".
func open(bucketURL, object string, clientOptions []option.ClientOption) (*store, error) {
	fail := func(err error) (*store, error) { return nil, fmt.Errorf("open bucket %v: %w", bucketURL, err) }
	u, err := url.Parse(bucketURL)
	if err != nil {
		return fail(err)
	}
	q := u.Query()
	for k := range q {
		if k != "access_id" && k != "private_key_path" && k != "anonymous" {
			return fail(fmt.Errorf("invalid query parameter %q", k))
		}
	}
	s := &store{bucket: u.Host, object: object, clientOptions: clientOptions}
	if anon := q.Get("anonymous"); anon != "" {
		isAnon, err := strconv.ParseBool(anon)
		if err != nil {
			return fail(fmt.Errorf("invalid value %q for query parameter \"anonymous\": %w", anon, err))
		}
		s.anonymous = isAnon
	}
	if q.Get("access_id") == "-" {
		s.anonymous = true
	}
	if keyPath := q.Get("private_key_path"); keyPath != "" {
		if _, err := os.ReadFile(keyPath); err != nil {
			return fail(err)
		}
	}
	return s, nil
}

// client opens a client for one call; credentials are looked up when it is created.
func (s *store) client(ctx context.Context) (*storage.Client, error) {
	var opts []option.ClientOption
	if s.anonymous {
		opts = append(opts, option.WithoutAuthentication())
	}
	c, err := storage.NewClient(ctx, append(opts, s.clientOptions...)...)
	if err != nil {
		return nil, err
	}
	// a failed fetch is tried again on the next poll, so the client gives up early
	c.SetRetry(storage.WithMaxAttempts(maxAttempts))
	return c, nil
}

const maxAttempts = 3

// Identity is the generation of the object, which changes with its content.
func (s *store) Identity(ctx context.Context) (string, time.Time, error) {
	c, err := s.client(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	defer c.Close()
	attrs, err := c.Bucket(s.bucket).Object(s.object).Attrs(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	s.mu.Lock()
	s.generation = attrs.Generation
	s.mu.Unlock()
	return strconv.FormatInt(attrs.Generation, 10), attrs.Updated, nil
}

// Read reads the generation of the last Identity call, and fails when the object has moved on since.
func (s *store) Read(ctx context.Context) ([]byte, string, error) {
	c, err := s.client(ctx)
	if err != nil {
		return nil, "", err
	}
	defer c.Close()
	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()
	obj := c.Bucket(s.bucket).Object(s.object)
	if generation != 0 {
		obj = obj.Generation(generation)
	}
	r, err := obj.NewReader(ctx)
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, "", err
	}
	return data, r.Attrs.ContentType, nil
}
