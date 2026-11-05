package evaluator_test

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/stretchr/testify/require"
)

func TestRequestWantedKeys(t *testing.T) {
	require.Nil(t, evaluator.Request{Bulk: true}.WantedKeys(), "a bulk request wants every key")
	require.Equal(t, map[string]struct{}{"a": {}}, evaluator.Request{FlagKey: "a"}.WantedKeys())
	require.Equal(t, map[string]struct{}{"a": {}, "b": {}},
		evaluator.Request{Bulk: true, FlagKey: "ignored", FlagKeys: []string{"a", "b"}}.WantedKeys(),
		"explicit keys win over the bulk flag and the single key")
	wanted := evaluator.Request{FlagKeys: []string{}}.WantedKeys()
	require.NotNil(t, wanted)
	require.Empty(t, wanted, "an empty key list wants no key")
}
