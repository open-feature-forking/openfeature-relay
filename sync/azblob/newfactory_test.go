package azblob

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewFactoriesMatchTypes(t *testing.T) {
	factories := NewFactories()
	require.Len(t, factories, 1)
	require.Equal(t, Provider, factories[0].Type())
}
