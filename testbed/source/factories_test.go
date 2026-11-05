package source

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewFactoriesServeDefault(t *testing.T) {
	factories := NewFactories()
	require.Len(t, factories, 1)
	require.Equal(t, Provider, factories[0].Type())
	require.Same(t, Default, factories[0].(*Factory).source)
}
