// Package gcs is the sync provider for flag configurations in Google Cloud Storage buckets. Importing
// it registers the gs:// scheme of gocloud's GCS driver, and no other cloud's driver.
package gcs

import (
	"regexp"

	"github.com/open-feature-forking/openfeature-relay/sync/blob"
	gocloud "gocloud.dev/blob"
	_ "gocloud.dev/blob/gcsblob" // needed to initialize GCS driver
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
func NewFactory() blob.Factory {
	return blob.Factory{Kind: Provider, Prefix: "gs://", Scheme: scheme, Mux: gocloud.DefaultURLMux()}
}
