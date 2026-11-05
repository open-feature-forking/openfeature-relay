package headers_test

import (
	"errors"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/headers"
	"github.com/stretchr/testify/require"
)

func TestFromSetting(t *testing.T) {
	h, err := headers.FromSetting(nil)
	require.NoError(t, err)
	require.Nil(t, h)

	in := map[string]string{"X-Key": "v"}
	h, err = headers.FromSetting(in)
	require.NoError(t, err)
	h["Other"] = "w"
	require.Equal(t, map[string]string{"X-Key": "v"}, in, "the result is a copy")

	h, err = headers.FromSetting(map[string]any{"X-Key": "v"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"X-Key": "v"}, h)

	h, err = headers.FromSetting(map[string]any{})
	require.NoError(t, err)
	require.NotNil(t, h)

	_, err = headers.FromSetting(map[string]any{"X-Key": 1})
	var notText *headers.NotTextError
	require.True(t, errors.As(err, &notText))
	require.Equal(t, "X-Key", notText.Name)
	require.EqualError(t, err, `the value of header "X-Key" is not a string`)

	_, err = headers.FromSetting([]string{"a"})
	var notMapping *headers.NotMappingError
	require.True(t, errors.As(err, &notMapping))
	require.EqualError(t, err, "expected a mapping of header names to strings, observed []string")
}

func TestValidName(t *testing.T) {
	for _, name := range []string{"X-Key", "authorization", "x_k.1", "!#$%&'*+-.^_`|~"} {
		require.True(t, headers.ValidName(name), name)
	}
	for _, name := range []string{"", "X Key", "X:Key", "X(Key)", "Ä", "X\r"} {
		require.False(t, headers.ValidName(name), name)
	}
}
