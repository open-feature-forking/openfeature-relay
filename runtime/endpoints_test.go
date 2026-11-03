package runtime

import (
	"context"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	"github.com/stretchr/testify/require"
)

type needyFactory struct {
	name  string
	needs service.Needs
}

func (f needyFactory) Type() string         { return f.name }
func (f needyFactory) Needs() service.Needs { return f.needs }
func (needyFactory) Create(service.Settings) (service.Endpoint, error) {
	return nil, nil
}

// countingEvaluators counts how many evaluators are created.
type countingEvaluator struct{ created *int }

func (countingEvaluator) Type() string                { return "counting" }
func (countingEvaluator) ConfigurationFormat() string { return "flagd" }
func (c countingEvaluator) Create(evaluator.Settings) (evaluator.Evaluator, error) {
	*c.created++
	return nil, nil
}

func inputsStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.NewStore(logger.NewLogger(nil, false), []string{"s"})
	require.NoError(t, err)
	return st
}

// TestNoEvaluatorWithoutNeed pins that no router and no evaluator are built when no endpoint needs one.
func TestNoEvaluatorWithoutNeed(t *testing.T) {
	created := 0
	in, err := newEndpointInputs(logger.NewLogger(nil, false), inputsStore(t), events.NewBus(),
		[]service.Factory{needyFactory{name: "sync-only"}}, []string{"flagd"}, []evaluator.Factory{countingEvaluator{&created}}, nil, false)
	require.NoError(t, err)

	require.Nil(t, in.router)
	require.Zero(t, created)
	s := in.settings(needyFactory{name: "sync-only"})
	require.Nil(t, s.Resolver)
	require.Nil(t, s.Evaluator)
}

func TestFormatNeedWithoutEvaluatorFailsStartup(t *testing.T) {
	_, err := newEndpointInputs(logger.NewLogger(nil, false), inputsStore(t), events.NewBus(),
		[]service.Factory{needyFactory{name: "vendor-api", needs: service.Needs{Format: "keyvalue"}}}, []string{"flagd"}, []evaluator.Factory{flagdeval.NewFactory()}, nil, false)
	require.EqualError(t, err, `the service endpoint "vendor-api" needs the evaluator of the configuration format "keyvalue", which the distribution lacks`)
}

// TestResolverPublishesAnEvaluationEvent pins that the runtime's own typed adapter of an endpoint publishes the
// evaluation events, so the endpoint does not.
func TestResolverPublishesAnEvaluationEvent(t *testing.T) {
	bus := events.NewBus()
	sub := bus.Subscribe(events.SubscribeOptions{Kinds: []events.Kind{events.KindEvaluation}, Buffer: 4})
	defer sub.Close()
	factory := needyFactory{name: "rest", needs: service.Needs{Router: true}}
	in, err := newEndpointInputs(logger.NewLogger(nil, false), inputsStore(t), bus, []service.Factory{factory}, []string{"flagd"}, []evaluator.Factory{flagdeval.NewFactory()}, nil, false)
	require.NoError(t, err)

	_, _, _, _, err = in.settings(factory).Resolver.ResolveBooleanValue(context.Background(), "", "missing", nil)
	require.EqualError(t, err, model.FlagNotFoundErrorCode)

	ev := (<-sub.C()).(events.Evaluation)
	require.Equal(t, "rest", ev.Endpoint)
	require.Equal(t, "missing", ev.FlagKey)
	require.Equal(t, model.FlagNotFoundErrorCode, ev.ErrorCode)
}

// TestSourceWithoutFormatNeedsFlagdFormat pins that a distribution without flagd's configuration format fails a
// source that names none, and lists the formats it has.
func TestSourceWithoutFormatNeedsFlagdFormat(t *testing.T) {
	log := logger.NewLogger(nil, false)
	_, err := defaultFormatOf(log, inputsStore(t), []sync.SourceConfig{{URI: "file:flags.json"}}, nil)
	require.ErrorContains(t, err, `source "file:flags.json" names no configuration format`)
	require.ErrorContains(t, err, "available formats")
}

// TestStrictNumericRangeReachesEvaluatorAndAdapter pins that the operator's strict numeric range reaches both the
// evaluator, through its factory settings, and the typed adapter of the endpoint.
func TestStrictNumericRangeReachesEvaluatorAndAdapter(t *testing.T) {
	st := inputsStore(t)
	st.Update("s", []model.Flag{{
		Key: "half", State: "ENABLED", DefaultVariant: "on",
		Variants: map[string]any{"on": 0.5},
	}}, nil, false)
	factory := needyFactory{name: "rest", needs: service.Needs{Router: true}}
	settings := func(string) map[string]any { return map[string]any{"strictNumericRange": true} }
	in, err := newEndpointInputs(logger.NewLogger(nil, false), st, events.NewBus(), []service.Factory{factory},
		[]string{"flagd"}, []evaluator.Factory{flagdeval.NewFactory()}, settings, true)
	require.NoError(t, err)

	_, _, _, _, err = in.settings(factory).Resolver.ResolveIntValue(context.Background(), "", "half", nil)
	require.Error(t, err)
}
