package evaluator

import (
	"errors"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// Settings is what a distribution hands an evaluator factory. There is no telemetry: an evaluator reports
// through its results.
type Settings struct {
	Store  *store.Store
	Logger *logger.Logger
	// Config holds the evaluator's own settings.
	Config map[string]any
}

// Factory creates an evaluator.
type Factory interface {
	// Type names the evaluator, for example in a distribution's configuration.
	Type() string
	// ConfigurationFormat names the configuration format whose flags the evaluator resolves.
	ConfigurationFormat() string
	Create(Settings) (Evaluator, error)
}

// ValidateDeclarations checks what a factory declares: a non-empty type name and a non-empty name of the
// configuration format whose flags the evaluator resolves.
func ValidateDeclarations(f Factory) error {
	if f == nil {
		return errors.New("the factory is nil")
	}
	if strings.TrimSpace(f.Type()) == "" {
		return errors.New("the type name is empty")
	}
	if strings.TrimSpace(f.ConfigurationFormat()) == "" {
		return errors.New("the configuration format name is empty")
	}
	return nil
}
