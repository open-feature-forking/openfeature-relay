package gcs

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

func Test_GcsConfig(t *testing.T) {
	lg := logger.NewLogger(nil, false)
	defaultInterval := uint32(5)
	tests := []struct {
		name             string
		uri              string
		interval         uint32
		expectedBucket   string
		expectedObject   string
		expectedInterval uint32
	}{
		{
			name:             "simple path",
			uri:              "gs://bucket/path/to/object",
			interval:         10,
			expectedBucket:   "gs://bucket/",
			expectedObject:   "path/to/object",
			expectedInterval: 10,
		},
		{
			name:             "default interval",
			uri:              "gs://bucket/path/to/object",
			expectedBucket:   "gs://bucket/",
			expectedObject:   "path/to/object",
			expectedInterval: defaultInterval,
		},
		{
			name:             "no object set", // Blob syncer will return error when fetching
			uri:              "gs://bucket/",
			expectedBucket:   "gs://bucket/",
			expectedObject:   "",
			expectedInterval: defaultInterval,
		},
		{
			name:             "malformed uri", // Blob syncer will return error when opening bucket
			uri:              "malformed",
			expectedBucket:   "",
			expectedObject:   "malformed",
			expectedInterval: defaultInterval,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gcsSync, err := New(sync.SourceConfig{
				URI:      tt.uri,
				Interval: tt.interval,
			}, lg, nil)
			require.NoError(t, err)
			require.Equal(t, tt.expectedBucket, gcsSync.Bucket)
			require.Equal(t, tt.expectedObject, gcsSync.Object)
			require.Equal(t, int(tt.expectedInterval), int(gcsSync.Interval))
		})
	}
}
