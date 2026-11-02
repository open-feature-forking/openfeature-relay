package builder

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/blob"
	"github.com/stretchr/testify/require"
)

// TestBlobSourcesCharacterization pins what the builder makes of blob sources, through its public entry
// point: the bucket URI and object name a source is split into, its polling interval, and the errors
// it is refused with. The bucket and object are what the store's source name is rebuilt from.
func TestBlobSourcesCharacterization(t *testing.T) {
	tests := []struct {
		name           string
		config         sync.SourceConfig
		storageAccount string // AZURE_STORAGE_ACCOUNT
		wantBucket     string
		wantObject     string
		wantInterval   uint32
		wantScheme     string // the URL scheme the cloud's driver registers on the mux
		wantErr        string
	}{
		{
			name:         "gcs",
			config:       sync.SourceConfig{URI: "gs://bucket/path/to/object", Provider: "gcs"},
			wantBucket:   "gs://bucket/",
			wantObject:   "path/to/object",
			wantInterval: 5,
			wantScheme:   "gs",
		},
		{
			name:         "gcs with interval",
			config:       sync.SourceConfig{URI: "gs://bucket/object", Provider: "gcs", Interval: 30},
			wantBucket:   "gs://bucket/",
			wantObject:   "object",
			wantInterval: 30,
			wantScheme:   "gs",
		},
		{
			name:    "gcs interval beyond the maximum",
			config:  sync.SourceConfig{URI: "gs://bucket/object", Provider: "gcs", Interval: 86401},
			wantErr: "could not create sync provider: invalid gcs sync configuration: polling interval 86401s exceeds maximum 86400s",
		},
		{
			name:           "azblob",
			config:         sync.SourceConfig{URI: "azblob://bucket/path/to/object", Provider: "azblob"},
			storageAccount: "account",
			wantBucket:     "azblob://bucket/",
			wantObject:     "path/to/object",
			wantInterval:   5,
			wantScheme:     "azblob",
		},
		{
			name:    "azblob without storage account",
			config:  sync.SourceConfig{URI: "azblob://bucket/object", Provider: "azblob"},
			wantErr: "could not create sync provider: environment variable AZURE_STORAGE_ACCOUNT not set or is blank",
		},
		{
			name:           "azblob with whitespace in the storage account",
			config:         sync.SourceConfig{URI: "azblob://bucket/object", Provider: "azblob"},
			storageAccount: "my account",
			wantErr:        "could not create sync provider: environment variable AZURE_STORAGE_ACCOUNT contains whitespace",
		},
		{
			name:         "s3",
			config:       sync.SourceConfig{URI: "s3://bucket/path/to/object", Provider: "s3", Interval: 10},
			wantBucket:   "s3://bucket/",
			wantObject:   "path/to/object",
			wantInterval: 10,
			wantScheme:   "s3",
		},
		{
			name:         "s3 query moves onto the bucket",
			config:       sync.SourceConfig{URI: "s3://bucket/path/to/object?use_path_style=true&region=eu-west-1", Provider: "s3"},
			wantBucket:   "s3://bucket?use_path_style=true&region=eu-west-1",
			wantObject:   "path/to/object",
			wantInterval: 5,
			wantScheme:   "s3",
		},
		{
			name:         "scheme without a slash after the bucket",
			config:       sync.SourceConfig{URI: "s3://bucket", Provider: "s3"},
			wantBucket:   "",
			wantObject:   "s3://bucket",
			wantInterval: 5,
			wantScheme:   "s3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZURE_STORAGE_ACCOUNT", tt.storageAccount)

			syncs, err := NewSyncBuilder().SyncsFromConfig([]sync.SourceConfig{tt.config}, logger.NewLogger(nil, false))

			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Len(t, syncs, 1)
			blobSync, ok := syncs[0].(*blob.Sync)
			require.True(t, ok, "want a blob sync, got %T", syncs[0])
			require.Equal(t, tt.wantBucket, blobSync.Bucket)
			require.Equal(t, tt.wantObject, blobSync.Object)
			require.Equal(t, tt.wantInterval, blobSync.Interval)
			require.True(t, blobSync.BlobURLMux.ValidBucketScheme(tt.wantScheme), "driver for %q is registered", tt.wantScheme)
		})
	}
}
