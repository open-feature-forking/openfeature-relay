package evaluator

import (
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
