package flagd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewFactoryMatchesType(t *testing.T) {
	factory := NewFactory()
	require.Equal(t, FactoryType, factory.Type())
	require.Equal(t, "flagd", factory.ConfigurationFormat())
}
