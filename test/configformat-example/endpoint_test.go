package keyvalue_test

import (
	"context"
	"testing"
	"time"

	keyvalue "github.com/open-feature/flagd/test/configformat-example"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"
)

// keyvalueEvaluator is the evaluator of the keyvalue format: a flag resolves to the raw JSON text of its
// value, and the flag set (the environment) is its metadata. The paired evaluator module is UC-007's.
type keyvalueEvaluator struct {
	store  *store.Store
	source string
}

func (e keyvalueEvaluator) Evaluate(_ context.Context, req evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	for _, d := range e.store.Documents(keyvalue.Type, e.source) {
		values, _ := d.Value.(keyvalue.Values)
		if value, ok := values.Get(req.FlagKey); ok {
			return append(dst, evaluator.Result{
				FlagKey: req.FlagKey, Value: value, HasValue: true, Variant: "set", Reason: model.StaticReason,
				Format: keyvalue.Type, Metadata: model.Metadata{"flagSetId": d.FlagSet},
			}), nil, nil
		}
	}
	return append(dst, evaluator.Result{FlagKey: req.FlagKey, ErrorCode: model.FlagNotFoundErrorCode, Format: keyvalue.Type}), nil, nil
}

// vendorEndpointFactory is a test endpoint in the shape of a vendor's client API (A2): it declares that it needs
// the evaluator of the keyvalue format and answers with that evaluator's own result, the environment of the
// flag, which the typed calls would not carry.
type vendorEndpointFactory struct{}

func (vendorEndpointFactory) Type() string         { return "vendor-api" }
func (vendorEndpointFactory) Needs() service.Needs { return service.Needs{Format: keyvalue.Type} }

func (vendorEndpointFactory) Create(s service.Settings) (service.Endpoint, error) {
	return &vendorEndpoint{settings: s}, nil
}

type vendorEndpoint struct{ settings service.Settings }

func (*vendorEndpoint) Serve(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Answer is what the endpoint tells a client that asks for a flag: the native result of the format's
// evaluator, and an evaluation event for the flag.
func (e *vendorEndpoint) Answer(ctx context.Context, key string) (value, environment string, found bool) {
	results, _, _ := e.settings.Evaluator.Evaluate(ctx, evaluator.Request{FlagKey: key}, nil)
	result := results[0]
	events.PublishEvaluation(e.settings.Bus, events.Evaluation{
		Time: time.Now(), FlagKey: key, FlagSet: environmentOf(result), Format: result.Format, Endpoint: "vendor-api",
		Variant: result.Variant, Reason: result.Reason, ErrorCode: result.ErrorCode, Value: result.Value,
		HasValue: result.HasValue, Origin: events.OriginService,
	})
	if !result.HasValue {
		return "", "", false
	}
	return result.Value.(string), environmentOf(result), true
}

func environmentOf(r evaluator.Result) string {
	environment, _ := r.Metadata["flagSetId"].(string)
	return environment
}

// TestVendorEndpointUsesFormatEvaluator is A2: an endpoint that needs the evaluator of a format gets exactly that
// evaluator, builds its answer from the evaluator's own result, and still publishes an evaluation event for
// each resolved flag.
func TestVendorEndpointUsesFormatEvaluator(t *testing.T) {
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"vendor-source"})
	require.NoError(t, err)
	format, err := keyvalue.Factory{}.Create(configformat.Settings{Store: st, Logger: log})
	require.NoError(t, err)
	_, err = format.Accept(context.Background(), configformat.Input{
		Source: "vendor-source", Data: `{"environment":"prod","flags":{"colour":"\"blue\"","limit":5}}`,
	})
	require.NoError(t, err)

	factory := vendorEndpointFactory{}
	require.NoError(t, service.ValidateDeclarations(factory))
	require.Equal(t, service.Needs{Format: "keyvalue"}, factory.Needs())

	bus := events.NewBus()
	sub := bus.Subscribe(events.SubscribeOptions{Kinds: []events.Kind{events.KindEvaluation}, Buffer: 8})
	defer sub.Close()
	// the runtime injects the evaluator of the format the endpoint named, and no resolver
	created, err := factory.Create(service.Settings{
		Store: st, Bus: bus, Logger: log, Evaluator: keyvalueEvaluator{store: st, source: "vendor-source"},
	})
	require.NoError(t, err)
	endpoint := created.(*vendorEndpoint)
	require.Nil(t, endpoint.settings.Resolver)

	value, environment, found := endpoint.Answer(context.Background(), "limit")
	require.True(t, found)
	require.Equal(t, "5", value, "the evaluator's native result, the raw value")
	require.Equal(t, "prod", environment, "what the typed calls do not carry")

	_, _, found = endpoint.Answer(context.Background(), "missing")
	require.False(t, found)

	first := (<-sub.C()).(events.Evaluation)
	require.Equal(t, events.Evaluation{
		Time: first.Time, FlagKey: "limit", FlagSet: "prod", Format: "keyvalue", Endpoint: "vendor-api", Variant: "set",
		Reason: model.StaticReason, Value: "5", HasValue: true, Origin: events.OriginService,
	}, first)
	second := (<-sub.C()).(events.Evaluation)
	require.Equal(t, "missing", second.FlagKey)
	require.Equal(t, model.FlagNotFoundErrorCode, second.ErrorCode)
	require.Empty(t, sub.C(), "one event per resolved flag")
}
