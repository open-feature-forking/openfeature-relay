package evaluator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type declared struct{ typ, format string }

func (d declared) Type() string                { return d.typ }
func (d declared) ConfigurationFormat() string { return d.format }
func (declared) Create(Settings) (Evaluator, error) {
	return nil, nil
}

func TestValidateDeclarations(t *testing.T) {
	require.NoError(t, ValidateDeclarations(declared{"jsonlogic", "flagd"}))
	require.EqualError(t, ValidateDeclarations(declared{" ", "flagd"}), "the type name is empty")
	require.EqualError(t, ValidateDeclarations(declared{"jsonlogic", ""}), "the configuration format name is empty")
	require.EqualError(t, ValidateDeclarations(nil), "the factory is nil")
}
