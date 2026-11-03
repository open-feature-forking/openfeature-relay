// Package azblob is the sync provider for flag configurations in Azure Blob Storage containers.
// Importing it registers the azblob:// scheme of gocloud's Azure driver, and no other cloud's driver.
package azblob

import (
	"fmt"
	"os"
	"regexp"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/sync/blob"
	gocloud "gocloud.dev/blob"
	_ "gocloud.dev/blob/azureblob" // needed to initialize Azure Blob Storage driver
)

// Provider is the name by which a source selects this sync provider.
const Provider = "azblob"

// scheme matches through the first "/" after the scheme, e.g. "azblob://bucket/".
var scheme = regexp.MustCompile("^azblob://.+?/")

// Matches reports whether uri is an azblob:// URI.
func Matches(uri string) bool {
	return scheme.MatchString(uri)
}

// New builds the sync for an azblob://bucket/path/to/object source: azblob://bucket/ is the bucket
// URI and path/to/object the object name. The storage account comes from AZURE_STORAGE_ACCOUNT.
func New(config sync.SourceConfig, log *logger.Logger, recorder sync.MetricsRecorder) (*blob.Sync, error) {
	// Required to generate the azblob service URL
	storageAccountName := os.Getenv("AZURE_STORAGE_ACCOUNT")
	if storageAccountName == "" {
		return nil, fmt.Errorf("environment variable AZURE_STORAGE_ACCOUNT not set or is blank")
	}
	if regexp.MustCompile(`\s`).MatchString(storageAccountName) {
		return nil, fmt.Errorf("environment variable AZURE_STORAGE_ACCOUNT contains whitespace")
	}

	return blob.NewSync(Provider, scheme, config, log, recorder, gocloud.DefaultURLMux())
}
