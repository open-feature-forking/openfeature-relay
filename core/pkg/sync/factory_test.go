package sync

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type declaredFactory struct {
	typ     string
	schemes []string
}

func (f declaredFactory) Type() string      { return f.typ }
func (f declaredFactory) Schemes() []string { return f.schemes }
func (f declaredFactory) ParseURI(string) (SourceConfig, bool) {
	return SourceConfig{}, false
}

type typeOnlyFactory struct{ typ string }

func (f typeOnlyFactory) Type() string { return f.typ }

func TestValidateDeclarations(t *testing.T) {
	tests := []struct {
		name    string
		factory Factory
		wantErr string
	}{
		{"a fetcher or streaming factory", declaredFactory{"consul", []string{"consul://"}}, ""},
		{"a trigger factory has no schemes", typeOnlyFactory{"sse"}, ""},
		{"empty type", declaredFactory{"", []string{"consul://"}}, "the type name is empty"},
		{"blank type", typeOnlyFactory{"  "}, "the type name is empty"},
		{"no scheme", declaredFactory{"consul", nil}, "no scheme is declared"},
		{"empty scheme", declaredFactory{"consul", []string{"consul://", ""}}, "scheme 2 is empty"},
		{"every broken rule", declaredFactory{"", []string{""}}, "the type name is empty; scheme 1 is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDeclarations(tt.factory)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}
