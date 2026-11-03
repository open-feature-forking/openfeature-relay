// Package azblob is the sync provider for flag configurations in Azure Blob Storage containers: a fetcher on the
// Azure SDK's azblob client.
//
// A source is "azblob://container/path/to/blob". The storage account and how to authenticate come from the
// environment:
//
//   - AZURE_STORAGE_ACCOUNT names the account (required);
//   - AZURE_STORAGE_KEY authenticates with the account's shared key;
//   - else AZURE_STORAGE_SAS_TOKEN is appended to the service URL;
//   - else AZURE_STORAGE_CONNECTION_STRING or AZURE_STORAGEBLOB_CONNECTIONSTRING authenticates with a connection
//     string;
//   - else the default Azure credential chain is used.
//
// AZURE_STORAGE_DOMAIN, AZURE_STORAGE_PROTOCOL, AZURE_STORAGE_IS_CDN and AZURE_STORAGE_IS_LOCAL_EMULATOR shape the
// service URL, and the query of a source overrides them: domain, protocol, cdn, localemu and storage_account.
package azblob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/blobfetch"
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
func NewFactory() blobfetch.Factory {
	return factory(nil)
}

// factory creates the factory with extra options for the container client, which the tests use to reach a stub.
func factory(clientOptions *container.ClientOptions) blobfetch.Factory {
	return blobfetch.Factory{
		Kind: Provider, Prefix: "azblob://", Scheme: scheme,
		Open: func(bucketURL, object string) (blobfetch.Store, error) {
			if err := checkEnvironment(); err != nil {
				return nil, err
			}
			return open(bucketURL, object, clientOptions)
		},
	}
}

// serviceURLOptions shape the URL of the storage account's blob service.
type serviceURLOptions struct {
	accountName, sasToken, domain, protocol string
	isCDN, isLocalEmulator                  bool
}

func serviceURLOptionsFromEnvironment() serviceURLOptions {
	isCDN, _ := strconv.ParseBool(os.Getenv("AZURE_STORAGE_IS_CDN"))
	isLocalEmulator, _ := strconv.ParseBool(os.Getenv("AZURE_STORAGE_IS_LOCAL_EMULATOR"))
	return serviceURLOptions{
		accountName:     os.Getenv("AZURE_STORAGE_ACCOUNT"),
		sasToken:        os.Getenv("AZURE_STORAGE_SAS_TOKEN"),
		domain:          os.Getenv("AZURE_STORAGE_DOMAIN"),
		protocol:        os.Getenv("AZURE_STORAGE_PROTOCOL"),
		isCDN:           isCDN,
		isLocalEmulator: isLocalEmulator,
	}
}

// withOverrides applies the query options of a source.
func (o serviceURLOptions) withOverrides(query url.Values) (serviceURLOptions, error) {
	for param, values := range query {
		if len(values) > 1 {
			return o, fmt.Errorf("multiple values of %v not allowed", param)
		}
		value := values[0]
		switch param {
		case "domain":
			o.domain = value
		case "protocol":
			o.protocol = value
		case "cdn":
			isCDN, err := strconv.ParseBool(value)
			if err != nil {
				return o, err
			}
			o.isCDN = isCDN
		case "localemu":
			isLocalEmulator, err := strconv.ParseBool(value)
			if err != nil {
				return o, err
			}
			o.isLocalEmulator = isLocalEmulator
		case "storage_account":
			o.accountName = value
		default:
			return o, fmt.Errorf("unknown query parameter %q", param)
		}
	}
	return o, nil
}

// serviceURL is "<protocol>://<account>.<domain>"; for a CDN the account is dropped, and for a local emulator or a
// domain on localhost the account follows the domain as a path. A SAS token is appended as the query.
func (o serviceURLOptions) serviceURL() (string, error) {
	if o.accountName == "" {
		return "", errors.New("azureblob: Options.AccountName is required")
	}
	domain := o.domain
	if domain == "" {
		domain = "blob.core.windows.net"
	}
	protocol := o.protocol
	if protocol == "" {
		protocol = "https"
	} else if protocol != "http" && protocol != "https" {
		return "", fmt.Errorf("invalid protocol %q", protocol)
	}
	var svcURL string
	switch {
	case strings.HasPrefix(domain, "127.0.0.1") || strings.HasPrefix(domain, "localhost") || o.isLocalEmulator:
		svcURL = fmt.Sprintf("%s://%s/%s", protocol, domain, o.accountName)
	case o.isCDN:
		svcURL = fmt.Sprintf("%s://%s", protocol, domain)
	default:
		svcURL = fmt.Sprintf("%s://%s.%s", protocol, o.accountName, domain)
	}
	if o.sasToken != "" {
		svcURL += "?" + o.sasToken
	}
	return svcURL, nil
}

// newContainerClient authenticates as the environment says: the shared key of the account, else the SAS token in the
// service URL, else a connection string, else the default Azure credential.
func newContainerClient(svcURL, containerName string, opts *container.ClientOptions) (*container.Client, error) {
	accountName := os.Getenv("AZURE_STORAGE_ACCOUNT")
	accountKey := os.Getenv("AZURE_STORAGE_KEY")
	connectionString := os.Getenv("AZURE_STORAGE_CONNECTION_STRING")
	if connectionString == "" {
		connectionString = os.Getenv("AZURE_STORAGEBLOB_CONNECTIONSTRING")
	}

	containerURL, err := url.JoinPath(svcURL, containerName)
	if err != nil {
		return nil, err
	}
	switch {
	case accountName != "" && accountKey != "":
		credential, err := azblob.NewSharedKeyCredential(accountName, accountKey)
		if err != nil {
			return nil, fmt.Errorf("failed azblob.NewSharedKeyCredential: %v", err)
		}
		return container.NewClientWithSharedKeyCredential(containerURL, credential, opts)
	case os.Getenv("AZURE_STORAGE_SAS_TOKEN") != "":
		return container.NewClientWithNoCredential(containerURL, opts)
	case connectionString != "":
		return container.NewClientFromConnectionString(connectionString, containerName, opts)
	default:
		credential, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("failed azidentity.NewDefaultAzureCredential: %v", err)
		}
		return container.NewClient(containerURL, credential, opts)
	}
}

// store is a blob of a container. The ETag it reads is the one its last Identity call saw, so that what is read is
// what was compared.
type store struct {
	client *container.Client
	blob   string

	etag *azcore.ETag
}

// open creates the client of a container from a bucket URL, "azblob://container?domain=...".
func open(bucketURL, object string, clientOptions *container.ClientOptions) (*store, error) {
	u, err := url.Parse(bucketURL)
	if err != nil {
		return nil, err
	}
	opts, err := serviceURLOptionsFromEnvironment().withOverrides(u.Query())
	if err != nil {
		return nil, err
	}
	svcURL, err := opts.serviceURL()
	if err != nil {
		return nil, err
	}
	options := &container.ClientOptions{}
	if clientOptions != nil {
		*options = *clientOptions
	}
	// a failed fetch is tried again on the next poll, so the client gives up early
	options.Retry = policy.RetryOptions{MaxRetries: 2, RetryDelay: 500 * time.Millisecond}
	client, err := newContainerClient(svcURL, u.Host, options)
	if err != nil {
		return nil, err
	}
	return &store{client: client, blob: object}, nil
}

// Identity is the ETag of the blob.
func (s *store) Identity(ctx context.Context) (string, time.Time, error) {
	props, err := s.client.NewBlobClient(s.blob).GetProperties(ctx, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	var etag string
	if props.ETag != nil {
		etag = string(*props.ETag)
	}
	s.etag = props.ETag
	var modified time.Time
	if props.LastModified != nil {
		modified = *props.LastModified
	}
	return etag, modified, nil
}

// Read reads the blob with the ETag of the last Identity call, and fails when the blob has changed since.
func (s *store) Read(ctx context.Context) ([]byte, string, error) {
	opts := &blob.DownloadStreamOptions{}
	if s.etag != nil {
		opts.AccessConditions = &blob.AccessConditions{
			ModifiedAccessConditions: &blob.ModifiedAccessConditions{IfMatch: s.etag},
		}
	}
	response, err := s.client.NewBlobClient(s.blob).DownloadStream(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, "", err
	}
	var contentType string
	if response.ContentType != nil {
		contentType = *response.ContentType
	}
	return data, contentType, nil
}

// NewFactories returns the factories of the azblob sync component, as a generated main of a distribution calls it.
func NewFactories() []sync.Factory { return []sync.Factory{NewFactory()} }
