package cmd

import (
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// TestConfigFileGRPCObjectIsStrict: a configuration file's sources are decoded without complaint about unknown keys, as
// before, but the `grpc` object of a source is read strictly.
func TestConfigFileGRPCObjectIsStrict(t *testing.T) {
	decode := func(t *testing.T, yaml string) ([]sync.SourceConfig, error) {
		t.Helper()
		v := viper.New()
		v.SetConfigType("yaml")
		require.NoError(t, v.ReadConfig(strings.NewReader(yaml)))
		var sources []sync.SourceConfig
		require.NoError(t, v.UnmarshalKey("sources", &sources))
		return sources, decodeGRPCSettings(v.Get("sources"), sources)
	}

	sources, err := decode(t, "sources:\n  - uri: up:8015\n    provider: grpc\n    keepAliveTime: 5000\n    grpc:\n      keepAliveTime: 30000\n      selectorHeader: true\n")
	require.NoError(t, err)
	require.Equal(t, &sync.GRPCSourceSettings{KeepAliveTime: 30000, SelectorHeader: true}, sources[0].GRPC, "the flat key stays ignored")

	sources, err = decode(t, "sources:\n  - uri: file:flags.json\n    provider: file\n    unknownKey: 1\n")
	require.NoError(t, err)
	require.Nil(t, sources[0].GRPC)

	_, err = decode(t, "sources:\n  - uri: up:8015\n    provider: grpc\n    grpc:\n      keepAliveTimeMs: 30000\n")
	require.Error(t, err)
	require.Contains(t, err.Error(), "up:8015")
	require.Contains(t, strings.ToLower(err.Error()), "keepalivetimems", "viper lower-cases the keys it reads")

	// a wrong type or a value that is no object is not left to the decoder of the configuration file
	for name, grpcObject := range map[string]string{
		"a wrong type":      "    grpc:\n      keepAliveTime: 30s\n",
		"not an object":     "    grpc: 5\n",
		"a wrong list type": "    grpc:\n      fatalStatusCodes: PERMISSION_DENIED\n",
	} {
		_, err = decode(t, "sources:\n  - uri: up:8015\n    provider: grpc\n"+grpcObject)
		require.Error(t, err, name)
		require.Contains(t, err.Error(), `source "up:8015"`, name)
		require.Contains(t, err.Error(), "grpc settings", name)
	}
}
