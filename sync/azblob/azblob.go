// Package azblob is the sync provider for flag configurations in Azure Blob Storage containers.
// Importing it registers the azblob:// scheme of gocloud's Azure driver, and no other cloud's driver.
package azblob

import (
	"fmt"
	"os"
	"regexp"

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

// checkEnvironment validates the storage account, which generates the azblob service URL.
func checkEnvironment() error {
	storageAccountName := os.Getenv("AZURE_STORAGE_ACCOUNT")
	if storageAccountName == "" {
		return fmt.Errorf("environment variable AZURE_STORAGE_ACCOUNT not set or is blank")
	}
	if regexp.MustCompile(`\s`).MatchString(storageAccountName) {
		return fmt.Errorf("environment variable AZURE_STORAGE_ACCOUNT contains whitespace")
	}
	return nil
}

// NewFactory creates the factory of azblob:// sources.
func NewFactory() blob.Factory {
	return blob.Factory{Kind: Provider, Prefix: "azblob://", Scheme: scheme, Mux: gocloud.DefaultURLMux(), Check: checkEnvironment}
}
