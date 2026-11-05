package build

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuoteFlag(t *testing.T) {
	require.Equal(t, "main.version=1.2.0", quoteFlag("main.version=1.2.0"))
	require.Equal(t, "'main.version=1.2.0 rc'", quoteFlag("main.version=1.2.0 rc"))
	require.Equal(t, `'main.version="x"'`, quoteFlag(`main.version="x"`))
	require.Equal(t, `"main.version=it's"`, quoteFlag("main.version=it's"))
	require.Equal(t, "'main.date=a\tb'", quoteFlag("main.date=a\tb"))
}

func TestRevisionOutsideARepository(t *testing.T) {
	require.Equal(t, "unknown", revision(t.TempDir()))
}
