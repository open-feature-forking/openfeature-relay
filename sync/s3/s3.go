// Package s3 is the sync provider for flag configurations in S3 buckets. Importing it registers the
// s3:// scheme of gocloud's S3 driver, and no other cloud's driver.
package s3

import (
	"regexp"

	"github.com/open-feature-forking/openfeature-relay/sync/blob"
	gocloud "gocloud.dev/blob"
	_ "gocloud.dev/blob/s3blob" // needed to initialize s3 driver
)

// Provider is the name by which a source selects this sync provider.
const Provider = "s3"

// scheme matches through the first "/" after the scheme, e.g. "s3://bucket/".
var scheme = regexp.MustCompile("^s3://.+?/")

// Matches reports whether uri is an s3:// URI.
func Matches(uri string) bool {
	return scheme.MatchString(uri)
}

// NewFactory creates the factory of s3:// sources.
func NewFactory() blob.Factory {
	return blob.Factory{Kind: Provider, Prefix: "s3://", Scheme: scheme, Mux: gocloud.DefaultURLMux()}
}
