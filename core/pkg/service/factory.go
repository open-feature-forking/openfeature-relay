package service

import (
	"errors"
	"strings"
)

// Factory creates a service endpoint from what a distribution injects.
type Factory interface {
	// Type names the service endpoint, for example in a distribution's configuration.
	Type() string
	// Needs declares what the endpoint resolves flags with. An endpoint that resolves no flag declares none.
	Needs() Needs
	Create(Settings) (Endpoint, error)
}

// ValidateDeclarations checks what a factory declares: a non-empty type name and no blank format need. The
// error names every broken rule.
func ValidateDeclarations(f Factory) error {
	var problems []string
	if strings.TrimSpace(f.Type()) == "" {
		problems = append(problems, "the type name is empty")
	}
	if needs := f.Needs(); needs.Format != strings.TrimSpace(needs.Format) {
		problems = append(problems, "the needed configuration format has surrounding whitespace")
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}
