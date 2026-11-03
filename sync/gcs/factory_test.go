package gcs

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

func TestFactory(t *testing.T) {
	f := NewFactory()
	require.Equal(t, "gcs", f.Type())
	require.Equal(t, "blob", f.SourceType())
	require.Equal(t, []string{"gs://"}, f.Schemes())

	got, ok := f.ParseURI("gs://bucket/path/flags.json")
	require.True(t, ok)
	require.Equal(t, sync.SourceConfig{URI: "gs://bucket/path/flags.json", Provider: "gcs"}, got)
	_, ok = f.ParseURI("file:/tmp/flags.json")
	require.False(t, ok)
}

func TestFactoryCreatesFetcher(t *testing.T) {
	t.Setenv("AZURE_STORAGE_ACCOUNT", "account")
	_, err := NewFactory().CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{URI: "gs://bucket/path/flags.json"}, Logger: logger.NewLogger(nil, false),
	})
	require.NoError(t, err)
}
