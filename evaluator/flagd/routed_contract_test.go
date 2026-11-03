package flagd

import (
	"context"
	"fmt"
	gosync "sync"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/evaluatortest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/router"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

// A key that flagd holds in two flag sets and another format wins in one of them is listed once for each flag
// set: flagd's entry of the flag set it keeps, the other format's entry of the one it won.
func TestMixedBulkKeepsTheEntryOfEachFlagSet(t *testing.T) {
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"flagd.json", "kv.json"})
	require.NoError(t, err)
	ev, err := NewRoutedEvaluator(log, st, []string{kvFormat}, []evaluator.Factory{kvFactory{kvFormat}})
	require.NoError(t, err)
	for _, set := range []string{"A", "B"} {
		require.NoError(t, ev.SetState(sync.DataSync{Source: "flagd.json", IncrementalUpdates: true, FlagData: fmt.Sprintf(`{
			"metadata": {"flagSetId": %q},
			"flags": {"dup": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": "flagd"}}}}`, set)}))
	}
	deliverKVSet(t, st, "kv.json", "B", "dup")

	bulk, _, err := ev.ResolveAllValues(context.Background(), "id", nil)
	require.NoError(t, err)
	var values []any
	for _, v := range bulk {
		require.Equal(t, "dup", v.FlagKey)
		values = append(values, v.Value)
	}
	require.ElementsMatch(t, []any{"flagd", "kv"}, values)
}

// routerFactory builds the router of a distribution with the JsonLogic evaluator and the kv one, so that the
// evaluator contract runs against the router itself.
type routerFactory struct{}

func (routerFactory) Type() string                { return "router" }
func (routerFactory) ConfigurationFormat() string { return "flagd" }
func (routerFactory) Create(s evaluator.Settings) (evaluator.Evaluator, error) {
	return router.New(s.Store,
		router.Route{Format: "flagd", Evaluator: NewJSON(s.Logger, s.Store)},
		router.Route{Format: kvFormat, Evaluator: kvEvaluator{st: s.Store}})
}

func TestRouterPassesTheContract(t *testing.T) {
	var mu gosync.Mutex
	writers := map[*store.Store]*JSON{}
	writer := func(st *store.Store) *JSON {
		mu.Lock()
		defer mu.Unlock()
		if w, ok := writers[st]; ok {
			return w
		}
		w := NewJSON(logger.NewLogger(nil, false), st)
		writers[st] = w
		return w
	}
	deliver := func(st *store.Store, data string, incremental bool) error {
		return writer(st).SetState(sync.DataSync{Source: evaluatortest.Source, FlagData: data, IncrementalUpdates: incremental})
	}
	flags := func(round int) string {
		return fmt.Sprintf(`{"flags": {
		  "static-bool": {"state": "ENABLED", "defaultVariant": "on", "variants": {"on": true, "off": false}},
		  "disabled": {"state": "DISABLED", "defaultVariant": "on", "variants": {"on": true, "off": false}},
		  "absent": {"state": "ENABLED", "variants": {"on": true, "off": false}},
		  "pair-a": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": %d}},
		  "pair-b": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": %d}}}}`, round, round)
	}
	evaluatortest.Run(t, routerFactory{}, evaluatortest.Samples{
		Setup: func(st *store.Store) error {
			if err := deliver(st, flags(0), false); err != nil {
				return err
			}
			for _, set := range []string{"A", "B"} {
				data := fmt.Sprintf(`{"metadata": {"flagSetId": %q}, "flags": {
				  "shared": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": %q}}}}`, set, set)
				if err := deliver(st, data, true); err != nil {
					return err
				}
			}
			return st.Apply(store.ApplyRequest{
				Format: kvFormat, Source: evaluatortest.Source,
				Documents: []store.Document{{Format: kvFormat, Source: evaluatortest.Source, FlagKeys: []string{"kv-key"}}},
			})
		},
		Static:        []evaluatortest.StaticSample{{Key: "static-bool", Hint: evaluator.TypeBool, Value: true}},
		Disabled:      "disabled",
		AbsentDefault: "absent",
		Shared:        &evaluatortest.SharedSample{Key: "shared", FlagSets: []string{"A", "B"}},
		Paired: &evaluatortest.PairedSample{
			Keys:  [2]string{"pair-a", "pair-b"},
			Write: func(st *store.Store, round int) error { return deliver(st, flags(round), false) },
			Value: func(r evaluator.Result) (int, bool) {
				v, ok := r.Value.(float64)
				return int(v), ok
			},
		},
	})
}
