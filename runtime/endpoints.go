package runtime

import (
	"errors"
	"fmt"
	"slices"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/router"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/typed"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
)

// endpointInputs builds what the service endpoint factories declared they need. The evaluation router, and
// with it every evaluator, exists only when a factory needs one.
type endpointInputs struct {
	router *router.Router
	store  *store.Store
	bus    *events.Bus
	log    *logger.Logger
	// typedOptions are those of every typed adapter the runtime builds.
	typedOptions []typed.Option
}

// newEndpointInputs builds the router when a factory needs it. A factory that needs the evaluator of a
// configuration format the distribution lacks fails the start, naming the endpoint and the format.
func newEndpointInputs(
	log *logger.Logger, st *store.Store, bus *events.Bus, factories []service.Factory,
	formats []string, evaluators []evaluator.Factory, evaluatorSettings func(evaluatorType string) map[string]any,
	strictNumericRange bool,
) (*endpointInputs, error) {
	in := &endpointInputs{store: st, bus: bus, log: log}
	// TODO(#2101): remove
	if strictNumericRange {
		in.typedOptions = append(in.typedOptions, typed.WithStrictNumericRange())
	}
	needsRouter := false
	for _, f := range factories {
		if needs := f.Needs(); needs.Router || needs.Format != "" {
			needsRouter = true
		}
	}
	if !needsRouter {
		return in, nil
	}
	rt, err := router.FromFactories(log, st, formats, evaluators, evaluatorSettings)
	if err != nil {
		return nil, err
	}
	in.router = rt

	for _, f := range factories {
		if format := f.Needs().Format; format != "" {
			if _, ok := rt.Evaluator(format); !ok {
				return nil, fmt.Errorf("the service endpoint %q needs the evaluator of the configuration format %q, which the distribution lacks",
					f.Type(), format)
			}
		}
	}
	return in, nil
}

// settings returns the settings of one factory with its resolver or evaluator, as it declared its needs. The
// resolver is the endpoint's own typed adapter, whose observer publishes an evaluation event for each flag.
func (in *endpointInputs) settings(f service.Factory) service.Settings {
	s := service.Settings{Store: in.store, Bus: in.bus, Logger: in.log}
	needs := f.Needs()
	if needs.Router {
		s.Resolver = in.resolver(typed.WithObserver(events.EvaluationObserver(in.bus, f.Type())))
	}
	if needs.Format != "" {
		s.Evaluator, _ = in.router.Evaluator(needs.Format)
	}
	return s
}

// resolver returns the typed calls over the router, with the options of the runtime and these.
func (in *endpointInputs) resolver(opts ...typed.Option) evaluator.IResolver {
	return typed.New(in.router, append(slices.Clone(in.typedOptions), opts...)...)
}

// stateless is the typed calls without a state to update: the configuration formats update the state, so a
// delivery to the evaluator itself is an error. It is what Runtime.Evaluator holds when the runtime builds it.
type stateless struct{ evaluator.IResolver }

//nolint:staticcheck // SA1019 SetState is the former way to update the state, kept on the interface
func (stateless) SetState(sync.DataSync) error {
	return errors.New("the configuration formats update the state, not the evaluator")
}

// evaluator returns the resolver of the runtime as an evaluator.IEvaluator, or nil without a router.
func (in *endpointInputs) evaluator() evaluator.IEvaluator {
	if in.router == nil {
		return nil
	}
	return stateless{in.resolver()}
}
