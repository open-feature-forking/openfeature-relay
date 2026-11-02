package azblob

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

func Test_AzblobConfig(t *testing.T) {
	lg := logger.NewLogger(nil, false)
	defaultInterval := uint32(5)
	tests := []struct {
		name             string
		uri              string
		interval         uint32
		storageAccount   string
		expectedBucket   string
		expectedObject   string
		expectedInterval uint32
		wantErr          bool
	}{
		{
			name:             "simple path",
			uri:              "azblob://bucket/path/to/object",
			interval:         10,
			storageAccount:   "myaccount",
			expectedBucket:   "azblob://bucket/",
			expectedObject:   "path/to/object",
			expectedInterval: 10,
			wantErr:          false,
		},
		{
			name:             "default interval",
			uri:              "azblob://bucket/path/to/object",
			storageAccount:   "myaccount",
			expectedBucket:   "azblob://bucket/",
			expectedObject:   "path/to/object",
			expectedInterval: defaultInterval,
			wantErr:          false,
		},
		{
			name:             "no object set", // Blob syncer will return error when fetching
			uri:              "azblob://bucket/",
			storageAccount:   "myaccount",
			expectedBucket:   "azblob://bucket/",
			expectedObject:   "",
			expectedInterval: defaultInterval,
			wantErr:          false,
		},
		{
			name:             "malformed uri", // Blob syncer will return error when opening bucket
			uri:              "malformed",
			storageAccount:   "myaccount",
			expectedBucket:   "",
			expectedObject:   "malformed",
			expectedInterval: defaultInterval,
			wantErr:          false,
		},
		{
			name:           "storage account not set", // Sync builder will fail and return error
			uri:            "azblob://bucket/path/to/object",
			storageAccount: "",
			wantErr:        true,
		},
		{
			name:           "storage account contains whitespace", // Sync builder will fail and return error
			uri:            "azblob://bucket/path/to/object",
			storageAccount: "my account",
			wantErr:        true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZURE_STORAGE_ACCOUNT", tt.storageAccount)
			azblobSync, err := New(sync.SourceConfig{
				URI:      tt.uri,
				Interval: tt.interval,
			}, lg, nil)

			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if (err != nil) && (tt.wantErr == true) {
				return
			}

			require.Equal(t, tt.expectedBucket, azblobSync.Bucket)
			require.Equal(t, tt.expectedObject, azblobSync.Object)
			require.Equal(t, int(tt.expectedInterval), int(azblobSync.Interval))
		})
	}
}
