package sync

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGRPCObjectDecodedStrictly(t *testing.T) {
	t.Run("a known object is read", func(t *testing.T) {
		var sources []SourceConfig
		require.NoError(t, json.Unmarshal([]byte(`[{"uri":"up:8015","provider":"grpc","selector":"a=b",
			"grpc":{"retryBackoffMs":1000,"retryBackoffMaxMs":5000,"streamDeadlineMs":600000,"keepAliveTime":30000,
			"fatalStatusCodes":["PERMISSION_DENIED"],"selectorHeader":true,"readyOnFirstPayload":true}}]`), &sources))
		require.Equal(t, &GRPCSourceSettings{
			RetryBackoffMs: 1000, RetryBackoffMaxMs: 5000, StreamDeadlineMs: 600000, KeepAliveTime: 30000,
			FatalStatusCodes: []string{"PERMISSION_DENIED"}, SelectorHeader: true, ReadyOnFirstPayload: true,
		}, sources[0].GRPC)
		require.Equal(t, "a=b", sources[0].Selector, "the rest of the source decodes as before")
	})

	for name, tc := range map[string]struct{ object, message string }{
		"an unknown key":    {`{"retryBackoffMs":1,"retryBackoffMax":5}`, "retryBackoffMax"},
		"a wrong type":      {`{"keepAliveTime":"30s"}`, "keepAliveTime"},
		"a wrong list type": {`{"fatalStatusCodes":"PERMISSION_DENIED"}`, "fatalStatusCodes"},
		"not an object":     {`[1]`, "grpc settings"},
	} {
		t.Run(name, func(t *testing.T) {
			var sources []SourceConfig
			err := json.Unmarshal([]byte(`[{"uri":"up:8015","provider":"grpc","grpc":`+tc.object+`}]`), &sources)
			require.Error(t, err)
			require.Contains(t, err.Error(), `source "up:8015"`)
			require.Contains(t, err.Error(), tc.message)
		})
	}

	t.Run("a configuration file's object is decoded the same way", func(t *testing.T) {
		settings, err := DecodeGRPCSettings(map[string]any{"retrybackoffms": 1000, "selectorheader": true})
		require.NoError(t, err)
		require.Equal(t, &GRPCSourceSettings{RetryBackoffMs: 1000, SelectorHeader: true}, settings)
		_, err = DecodeGRPCSettings(map[string]any{"nope": 1})
		require.ErrorContains(t, err, "nope")
		settings, err = DecodeGRPCSettings(nil)
		require.NoError(t, err)
		require.Nil(t, settings)
	})
}

// TestFlatProviderKeysStillIgnored pins that keys spelled as in the flagd providers' documentation, on the source
// itself, are ignored as they are today: only the `grpc` object is read.
func TestFlatProviderKeysStillIgnored(t *testing.T) {
	var sources []SourceConfig
	require.NoError(t, json.Unmarshal([]byte(`[{"uri":"up:8015","provider":"grpc","keepAliveTime":5000,
		"retryBackoffMaxMs":2000,"fatalStatusCodes":["PERMISSION_DENIED"],"streamDeadlineMs":1,"deadlineMs":1}]`), &sources))
	require.Equal(t, []SourceConfig{{URI: "up:8015", Provider: "grpc"}}, sources)
}

func TestEmptyGRPCObjectDecodes(t *testing.T) {
	var sources []SourceConfig
	require.NoError(t, json.Unmarshal([]byte(`[{"uri":"a","provider":"grpc","grpc":{}},{"uri":"b","provider":"grpc","grpc":null}]`), &sources))
	require.Equal(t, &GRPCSourceSettings{}, sources[0].GRPC)
	require.Nil(t, sources[1].GRPC)
}
