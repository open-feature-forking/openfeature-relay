package runtime

import (
	"fmt"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/typed"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	"github.com/open-feature/flagd/flagd/pkg/evaluation"
)

// endpointInputs builds what the service endpoint factories declared they need. The evaluation router, and
// with it every evaluator, exists only when a factory needs one.
type endpointInputs struct {
	router *evaluation.Router
	store  *store.Store
	bus    *events.Bus
	log    *logger.Logger
}

// newEndpointInputs builds the router when a factory needs it. A factory that needs the evaluator of a
// configuration format the distribution lacks fails the start, naming the endpoint and the format.
func newEndpointInputs(
	log *logger.Logger, st *store.Store, bus *events.Bus, factories []service.Factory,
	formats []string, extra []evaluator.Factory, opts []flagdeval.JSONEvaluatorOption,
) (*endpointInputs, error) {
	in := &endpointInputs{store: st, bus: bus, log: log}
	needsRouter := false
	for _, f := range factories {
		if needs := f.Needs(); needs.Router || needs.Format != "" {
			needsRouter = true
		}
	}
	if !needsRouter {
		return in, nil
	}
	rt, err := evaluation.NewRouter(log, st, formats, extra, opts...)
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
		s.Resolver = in.router.Resolver(typed.WithObserver(events.EvaluationObserver(in.bus, f.Type())))
	}
	if needs.Format != "" {
		s.Evaluator, _ = in.router.Evaluator(needs.Format)
	}
	return s
}
