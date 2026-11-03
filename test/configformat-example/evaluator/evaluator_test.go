package evaluator_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/evaluatortest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"

	keyvalue "github.com/open-feature/flagd/test/configformat-example"
	keyvalueeval "github.com/open-feature/flagd/test/configformat-example/evaluator"
)

func deliver(t testing.TB, st *store.Store, source, data string) {
	t.Helper()
	deliverWith(t, st, source, data, false)
}

func deliverWith(t testing.TB, st *store.Store, source, data string, incremental bool) {
	t.Helper()
	format, err := keyvalue.Factory{}.Create(configformat.Settings{Store: st, Logger: logger.NewLogger(nil, false)})
	require.NoError(t, err)
	_, err = format.Accept(context.Background(), configformat.Input{Source: source, Data: data, IncrementalUpdate: incremental})
	require.NoError(t, err)
}

func samples() evaluatortest.Samples {
	flags := func(round int) string {
		return fmt.Sprintf(`{"environment":"","flags":{
			"static-bool":true,"static-int":10,"static-string":"s","client.banner":"welcome","server.secret":"hush",
			"pair-a":%d,"pair-b":%d}}`, round, round)
	}
	deliverRound := func(st *store.Store, round int) error {
		format, err := keyvalue.Factory{}.Create(configformat.Settings{Store: st})
		if err != nil {
			return err
		}
		_, err = format.Accept(context.Background(), configformat.Input{Source: evaluatortest.Source, Data: flags(round)})
		return err
	}
	return evaluatortest.Samples{
		Setup: func(st *store.Store) error {
			if err := deliverRound(st, 0); err != nil {
				return err
			}
			format, err := keyvalue.Factory{}.Create(configformat.Settings{Store: st})
			if err != nil {
				return err
			}
			for _, env := range []string{"A", "B"} {
				data := fmt.Sprintf(`{"environment":%q,"flags":{"shared":%q}}`, env, env)
				if _, err := format.Accept(context.Background(), configformat.Input{
					Source: evaluatortest.Source, Data: data, IncrementalUpdate: true,
				}); err != nil {
					return err
				}
			}
			return nil
		},
		Shared: &evaluatortest.SharedSample{Key: "shared", FlagSets: []string{"A", "B"}},
		Static: []evaluatortest.StaticSample{
			{Key: "static-bool", Hint: evaluator.TypeBool, Value: true},
			{Key: "static-int", Hint: evaluator.TypeInt, Value: 10.0},
			{Key: "static-string", Hint: evaluator.TypeString, Value: "s"},
		},
		Attribute: &evaluatortest.AttributeSample{
			Key: "client.banner", Attributes: map[string]any{"audience": "client"}, With: "welcome",
		},
		Paired: &evaluatortest.PairedSample{
			Keys:  [2]string{"pair-a", "pair-b"},
			Write: deliverRound,
			Value: func(r evaluator.Result) (int, bool) {
				v, ok := r.Value.(float64)
				return int(v), ok
			},
		},
	}
}

// TestEvaluatorContract covers the main scenario's steps 1 to 6 and A1: the engine module's evaluator meets
// every case of the evaluator contract with samples written in the module's own format.
func TestEvaluatorContract(t *testing.T) {
	evaluatortest.Run(t, keyvalueeval.Factory{}, samples())
}

func TestFactory(t *testing.T) {
	var f evaluator.Factory = keyvalueeval.Factory{}
	require.Equal(t, keyvalue.Type, f.ConfigurationFormat())
	require.NotEmpty(t, f.Type())
	_, err := f.Create(evaluator.Settings{})
	require.Error(t, err)
}

func newEvaluator(t *testing.T, sources ...string) (evaluator.Evaluator, *store.Store) {
	t.Helper()
	st, err := store.NewStore(logger.NewLogger(nil, false), sources)
	require.NoError(t, err)
	ev, err := keyvalueeval.Factory{}.Create(evaluator.Settings{Store: st})
	require.NoError(t, err)
	return ev, st
}

func resolve(t *testing.T, ev evaluator.Evaluator, req evaluator.Request) map[string]any {
	t.Helper()
	results, _, err := ev.Evaluate(context.Background(), req, nil)
	require.NoError(t, err)
	out := map[string]any{}
	for _, r := range results {
		key := r.FlagKey
		if set, _ := r.Metadata["flagSetId"].(string); set != "" {
			key += "@" + set
		}
		if r.HasValue {
			out[key] = r.Value
		} else {
			out[key] = r.ErrorCode
		}
	}
	return out
}

// TestAttributesShapeResults covers A3's evaluator half: the attributes of a request decide which keys it sees,
// for single and bulk requests alike.
func TestAttributesShapeResults(t *testing.T) {
	ev, st := newEvaluator(t, "src")
	deliver(t, st, "src", `{"environment":"","flags":{"plain":1,"server.secret":"hush","client.banner":"welcome"}}`)

	client := map[string]any{"audience": "client"}
	// a hidden key is known and resolves to no value, so the caller's default stands
	noValue := func(ev evaluator.Evaluator, req evaluator.Request, key string) {
		t.Helper()
		results, _, err := ev.Evaluate(context.Background(), req, nil)
		require.NoError(t, err)
		for _, r := range results {
			if r.FlagKey == key {
				require.False(t, r.HasValue, key)
				require.Empty(t, r.ErrorCode, key)
				require.Equal(t, model.DefaultReason, r.Reason, key)
				return
			}
		}
		t.Fatalf("no result for %s", key)
	}

	require.Equal(t, "welcome", resolve(t, ev, evaluator.Request{Bulk: true, Attributes: client})["client.banner"])
	noValue(ev, evaluator.Request{Bulk: true, Attributes: client}, "server.secret")
	require.Equal(t, "hush", resolve(t, ev, evaluator.Request{Bulk: true})["server.secret"])
	noValue(ev, evaluator.Request{Bulk: true}, "client.banner")
	require.Equal(t, 1.0, resolve(t, ev, evaluator.Request{Bulk: true, Attributes: client})["plain"])

	noValue(ev, evaluator.Request{FlagKey: "server.secret", Attributes: client}, "server.secret")
	noValue(ev, evaluator.Request{FlagKey: "client.banner"}, "client.banner")
}

func TestSelectorAndSourcePrecedence(t *testing.T) {
	ev, st := newEvaluator(t, "low", "high")
	deliver(t, st, "low", `{"environment":"prod","flags":{"shared":"low","only-low":1}}`)
	deliver(t, st, "high", `{"environment":"prod","flags":{"shared":"high"}}`)
	deliverWith(t, st, "high", `{"environment":"dev","flags":{"shared":"dev"}}`, true)

	// the highest-priority source wins; between its flag sets the first in name order does
	require.Equal(t, map[string]any{"shared@dev": "dev"}, resolve(t, ev, evaluator.Request{FlagKey: "shared"}))
	require.Equal(t, map[string]any{"shared@prod": "high", "shared@dev": "dev", "only-low@prod": 1.0},
		resolve(t, ev, evaluator.Request{Bulk: true}), "one entry for each flag set holding a key")

	prod, err := store.NewSelector("flagSetId=prod")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"shared@prod": "high", "only-low@prod": 1.0},
		resolve(t, ev, evaluator.Request{Bulk: true, Selector: prod}))

	low, err := store.NewSelector("source=low")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"shared@prod": "low"}, resolve(t, ev, evaluator.Request{FlagKey: "shared", Selector: low}))

	require.Equal(t, map[string]any{"shared@dev": "dev", "only-low@prod": 1.0},
		resolve(t, ev, evaluator.Request{FlagKeys: []string{"shared", "only-low"}}))
}
