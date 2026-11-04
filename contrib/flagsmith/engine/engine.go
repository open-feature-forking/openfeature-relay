package flagsmith

import (
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
)

// NewConfigurationFormatFactory returns the factory of the engine's configuration format, as a generated main of a
// distribution calls it for an entry of the `engines` group.
func NewConfigurationFormatFactory() configformat.Factory { return Factory{} }

// NewEvaluatorFactory returns the factory of the engine's evaluator, the second half of an entry of the `engines`
// group.
func NewEvaluatorFactory() evaluator.Factory { return EvaluatorFactory{} }
