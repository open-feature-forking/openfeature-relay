package azblob

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/sync/blob"
	"github.com/stretchr/testify/require"
)

func Test_AzblobConfig(t *testing.T) {
	t.Setenv("AZURE_STORAGE_ACCOUNT", "myaccount")
	tests := []struct {
		name           string
		uri            string
		expectedBucket string
		expectedObject string
	}{
		{
			name:           "simple path",
			uri:            "azblob://bucket/path/to/object",
			expectedBucket: "azblob://bucket/",
			expectedObject: "path/to/object",
		},
		{
			name:           "no object set", // the fetcher fails Init
			uri:            "azblob://bucket/",
			expectedBucket: "azblob://bucket/",
			expectedObject: "",
		},
		{
			name:           "malformed uri", // the fetcher fails when opening the bucket
			uri:            "malformed",
			expectedBucket: "",
			expectedObject: "malformed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := NewFactory().CreateFetcher(sync.Settings{
				Source: sync.SourceConfig{URI: tt.uri}, Logger: logger.NewLogger(nil, false),
			})
			require.NoError(t, err)
			fetcher, ok := f.(*blob.Fetcher)
			require.True(t, ok)
			require.Equal(t, tt.expectedBucket, fetcher.Bucket())
			require.Equal(t, tt.expectedObject, fetcher.Object())
		})
	}
}
