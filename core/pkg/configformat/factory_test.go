package configformat

import "testing"

type declared string

func (d declared) Type() string                               { return string(d) }
func (declared) Create(Settings) (ConfigurationFormat, error) { return nil, nil }

func TestValidateDeclarations(t *testing.T) {
	if err := ValidateDeclarations(declared("keyvalue")); err != nil {
		t.Errorf("a named factory is valid: %v", err)
	}
	if err := ValidateDeclarations(declared(" ")); err == nil {
		t.Error("a blank type name is not valid")
	}
	if err := ValidateDeclarations(nil); err == nil {
		t.Error("a nil factory is not valid")
	}
}
