package azblob

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

func TestFactory(t *testing.T) {
	f := NewFactory()
	require.Equal(t, "azblob", f.Type())
	require.Equal(t, "blob", f.SourceType())
	require.Equal(t, []string{"azblob://"}, f.Schemes())

	got, ok := f.ParseURI("azblob://bucket/flags.yaml")
	require.True(t, ok)
	require.Equal(t, sync.SourceConfig{URI: "azblob://bucket/flags.yaml", Provider: "azblob"}, got)
	_, ok = f.ParseURI("file:/tmp/flags.json")
	require.False(t, ok)
}

func TestFactoryCreatesFetcher(t *testing.T) {
	t.Setenv("AZURE_STORAGE_ACCOUNT", "account")
	_, err := NewFactory().CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{URI: "azblob://bucket/flags.yaml"}, Logger: logger.NewLogger(nil, false),
	})
	require.NoError(t, err)
}

func TestFactoryChecksTheStorageAccount(t *testing.T) {
	settings := sync.Settings{Source: sync.SourceConfig{URI: "azblob://bucket/flags.yaml"}, Logger: logger.NewLogger(nil, false)}

	t.Setenv("AZURE_STORAGE_ACCOUNT", "")
	_, err := NewFactory().CreateFetcher(settings)
	require.EqualError(t, err, "environment variable AZURE_STORAGE_ACCOUNT not set or is blank")

	t.Setenv("AZURE_STORAGE_ACCOUNT", "my account")
	_, err = NewFactory().CreateFetcher(settings)
	require.EqualError(t, err, "environment variable AZURE_STORAGE_ACCOUNT contains whitespace")
}
