package flagd

import (
	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/router"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/typed"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
)

// NewEvaluator builds the evaluator over the given store: flagd's JsonLogic evaluator behind the evaluation
// router. Over a store of another kind the JsonLogic evaluator serves on its own.
func NewEvaluator(log *logger.Logger, s store.IStore, opts ...JSONEvaluatorOption) evaluator.IEvaluator {
	jsonEvaluator := NewJSON(log, s, opts...)
	st, ok := s.(*store.Store)
	if !ok {
		return jsonEvaluator
	}
	rt, err := router.New(st, router.Route{Format: flagdformat.Type, Evaluator: jsonEvaluator})
	if err != nil {
		// a single route with a format and an evaluator is always valid
		panic(err)
	}
	return routed{IResolver: typed.New(rt, typedOptions(jsonEvaluator)...), json: jsonEvaluator}
}

// NewRoutedEvaluator builds the evaluator of a distribution whose configuration formats are formats (besides
// flagd's own) and whose evaluators, besides flagd's, are extra. An evaluator for a configuration format the
// distribution lacks is an error that lists the formats; a format without an evaluator is logged, and its flags
// resolve to FLAG_NOT_FOUND.
func NewRoutedEvaluator(
	log *logger.Logger, st *store.Store, formats []string, extra []evaluator.Factory, opts ...JSONEvaluatorOption,
) (evaluator.IEvaluator, error) {
	jsonEvaluator := NewJSON(log, st, opts...)
	factories := append([]evaluator.Factory{instance{evaluator: jsonEvaluator}}, extra...)
	rt, err := router.FromFactories(log, st, append([]string{flagdformat.Type}, formats...), factories, nil)
	if err != nil {
		return nil, err
	}
	return routed{IResolver: typed.New(rt, typedOptions(jsonEvaluator)...), json: jsonEvaluator}, nil
}

// instance is the factory of an evaluator that exists already.
type instance struct{ evaluator *JSON }

func (instance) Type() string                { return FactoryType }
func (instance) ConfigurationFormat() string { return flagdformat.Type }
func (i instance) Create(evaluator.Settings) (evaluator.Evaluator, error) {
	return i.evaluator, nil
}

// routed is the typed calls over the router, with the state updates of flagd's evaluator.
type routed struct {
	evaluator.IResolver
	json *JSON
}

//nolint:staticcheck // SA1019 the runtime's fallback path for sources without a configuration format
func (r routed) SetState(payload sync.DataSync) error { return r.json.SetState(payload) }

func typedOptions(ev *JSON) []typed.Option {
	if ev.StrictNumericRange() {
		return []typed.Option{typed.WithStrictNumericRange()}
	}
	return nil
}
