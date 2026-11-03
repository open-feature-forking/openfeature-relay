package flagd

import (
	"errors"

	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
)

// FactoryType names the JsonLogic evaluator.
const FactoryType = "jsonlogic"

// Factory creates the JsonLogic evaluator for flagd's configuration format. Its settings:
// "strictNumericRange" (bool) enables WithStrictNumericRange for the typed calls made through it.
type Factory struct{}

var _ evaluator.Factory = Factory{}

func (Factory) Type() string { return FactoryType }

func (Factory) ConfigurationFormat() string { return flagdformat.Type }

func (Factory) Create(settings evaluator.Settings) (evaluator.Evaluator, error) {
	if settings.Store == nil || settings.Logger == nil {
		return nil, errors.New("the JsonLogic evaluator needs a store and a logger")
	}
	var opts []JSONEvaluatorOption
	if strict, _ := settings.Config["strictNumericRange"].(bool); strict {
		opts = append(opts, WithStrictNumericRange())
	}
	return NewJSON(settings.Logger, settings.Store, opts...), nil
}

// NewFactory returns the factory of the JsonLogic evaluator, as a generated main of a distribution calls it.
func NewFactory() evaluator.Factory { return Factory{} }
