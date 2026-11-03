// Package evaluation builds the evaluator flagd serves flags with.
package evaluation

import (
	"fmt"
	"sort"
	"strings"

	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/router"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/typed"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
)

// NewEvaluator builds the evaluator over the given store: flagd's JsonLogic evaluator behind the evaluation
// router. Over a store of another kind the JsonLogic evaluator serves on its own.
func NewEvaluator(log *logger.Logger, s store.IStore, opts ...flagdeval.JSONEvaluatorOption) evaluator.IEvaluator {
	jsonEvaluator := flagdeval.NewJSON(log, s, opts...)
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
	log *logger.Logger, st *store.Store, formats []string, extra []evaluator.Factory, opts ...flagdeval.JSONEvaluatorOption,
) (evaluator.IEvaluator, error) {
	available := append([]string{flagdformat.Type}, formats...)
	sort.Strings(available[1:])

	jsonEvaluator := flagdeval.NewJSON(log, st, opts...)
	routes := []router.Route{{Format: flagdformat.Type, Evaluator: jsonEvaluator}}
	covered := map[string]struct{}{flagdformat.Type: {}}
	for _, factory := range extra {
		format := factory.ConfigurationFormat()
		if !contains(available, format) {
			return nil, fmt.Errorf("the evaluator %q resolves the configuration format %q, which the distribution lacks; it has: %s",
				factory.Type(), format, strings.Join(available, ", "))
		}
		ev, err := factory.Create(evaluator.Settings{Store: st, Logger: log})
		if err != nil {
			return nil, fmt.Errorf("error creating the evaluator %q: %w", factory.Type(), err)
		}
		routes = append(routes, router.Route{Format: format, Evaluator: ev})
		covered[format] = struct{}{}
	}
	for _, format := range available {
		if _, ok := covered[format]; !ok {
			log.Warn(fmt.Sprintf("the configuration format %q has no evaluator; its flags resolve to FLAG_NOT_FOUND", format))
		}
	}

	rt, err := router.New(st, routes...)
	if err != nil {
		return nil, err
	}
	return routed{IResolver: typed.New(rt, typedOptions(jsonEvaluator)...), json: jsonEvaluator}, nil
}

// routed is the typed calls over the router, with the state updates of flagd's evaluator.
type routed struct {
	evaluator.IResolver
	json *flagdeval.JSON
}

//nolint:staticcheck // SA1019 the runtime's fallback path for sources without a configuration format
func (r routed) SetState(payload sync.DataSync) error { return r.json.SetState(payload) }

func typedOptions(ev *flagdeval.JSON) []typed.Option {
	if ev.StrictNumericRange() {
		return []typed.Option{typed.WithStrictNumericRange()}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
