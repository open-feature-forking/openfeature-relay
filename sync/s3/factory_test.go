package s3

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

func TestFactory(t *testing.T) {
	f := NewFactory()
	require.Equal(t, "s3", f.Type())
	require.Equal(t, "blob", f.SourceType())
	require.Equal(t, []string{"s3://"}, f.Schemes())

	got, ok := f.ParseURI("s3://bucket/flags.json?region=eu")
	require.True(t, ok)
	require.Equal(t, sync.SourceConfig{URI: "s3://bucket/flags.json?region=eu", Provider: "s3"}, got)
	_, ok = f.ParseURI("file:/tmp/flags.json")
	require.False(t, ok)
}

func TestFactoryCreatesFetcher(t *testing.T) {
	t.Setenv("AZURE_STORAGE_ACCOUNT", "account")
	_, err := NewFactory().CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{URI: "s3://bucket/flags.json?region=eu"}, Logger: logger.NewLogger(nil, false),
	})
	require.NoError(t, err)
}
