package keyvalue_test

import (
	"testing"

	keyvalue "github.com/open-feature/flagd/test/configformat-example"
	"github.com/stretchr/testify/require"
)

// TestEnginePairMatchesTypes pins the two factories an `engines` entry of a manifest is built from.
func TestEnginePairMatchesTypes(t *testing.T) {
	format, evaluator := keyvalue.NewConfigurationFormatFactory(), keyvalue.NewEvaluatorFactory()

	require.Equal(t, keyvalue.Type, format.Type())
	require.Equal(t, "keyvalue", evaluator.Type())
	require.Equal(t, format.Type(), evaluator.ConfigurationFormat(), "the evaluator resolves the format of the pair")
}
