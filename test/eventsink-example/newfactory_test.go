package eventsinkexample

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewFactoryMatchesType(t *testing.T) {
	require.Equal(t, Type, NewFactory().Type())
}
