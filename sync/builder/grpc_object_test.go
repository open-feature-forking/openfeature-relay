package builder

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

// TestGRPCObjectRejectedOnOtherProviders: a `grpc` object on a source that is not a gRPC source stops startup, naming
// the source.
func TestGRPCObjectRejectedOnOtherProviders(t *testing.T) {
	log := logger.NewLogger(nil, false)
	for _, source := range []sync.SourceConfig{
		{URI: "https://example.com/flags.json", Provider: "http", GRPC: &sync.GRPCSourceSettings{}},
		{URI: "file:flags.json", Provider: "file", GRPC: &sync.GRPCSourceSettings{KeepAliveTime: 10000}},
	} {
		t.Run(source.Provider, func(t *testing.T) {
			_, err := NewSyncBuilder().SyncsFromConfig([]sync.SourceConfig{source}, log)
			require.Error(t, err)
			require.Contains(t, err.Error(), source.URI)
			require.Contains(t, err.Error(), "grpc object")
		})
	}
}

// TestGRPCObjectParsedFromSources: the --sources JSON reads the object, and flat provider keys stay ignored.
func TestGRPCObjectParsedFromSources(t *testing.T) {
	sources, err := ParseSources(`[{"uri":"up:8015","provider":"grpc","keepAliveTime":5000,"grpc":{"keepAliveTime":10000}}]`)
	require.NoError(t, err)
	require.Equal(t, &sync.GRPCSourceSettings{KeepAliveTime: 10000}, sources[0].GRPC)

	_, err = ParseSources(`[{"uri":"up:8015","provider":"grpc","grpc":{"keepAliveTimeMs":10000}}]`)
	require.ErrorContains(t, err, "keepAliveTimeMs")
}
