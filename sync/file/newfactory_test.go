package file

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewFactoriesMatchTypes(t *testing.T) {
	var types []string
	for _, f := range NewFactories() {
		types = append(types, f.Type())
	}
	require.Equal(t, []string{Provider, FSNOTIFY, FILEINFO}, types)
}
