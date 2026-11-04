package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type textValue string

func (v textValue) DocumentText() string { return string(v) }

func TestDocumentText(t *testing.T) {
	t.Run("a value with a text", func(t *testing.T) {
		text, ok := DocumentText(Document{Value: textValue(`{"a":1}`)})
		require.True(t, ok)
		require.Equal(t, `{"a":1}`, text)
	})
	t.Run("an empty text is still a text", func(t *testing.T) {
		text, ok := DocumentText(Document{Value: textValue("")})
		require.True(t, ok)
		require.Empty(t, text)
	})
	t.Run("a value without one", func(t *testing.T) {
		for _, v := range []any{nil, "plain string", map[string]string{"a": "b"}} {
			_, ok := DocumentText(Document{Value: v})
			require.False(t, ok, "%T", v)
		}
	})
}
