package flagsmith_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"

	flagsmith "github.com/open-feature-forking/openfeature-relay/contrib/flagsmith/engine"
)

// keysAndSets returns "key@flagSet" of each result, in order.
func keysAndSets(results []evaluator.Result) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.FlagKey+"@"+r.FlagSet)
	}
	return out
}

// TestEvaluateTieBetweenFlagSets pins that a single key two flag sets of one source hold resolves in the flag set
// that sorts first, whatever order the environments were delivered in.
func TestEvaluateTieBetweenFlagSets(t *testing.T) {
	for name, order := range map[string][]string{"A then B": {"ENV-A", "ENV-B"}, "B then A": {"ENV-B", "ENV-A"}} {
		t.Run(name, func(t *testing.T) {
			ev, st := newEvaluator(t, nil, "src")
			for _, env := range order {
				deliver(t, st, "src", envDoc(env, false, feat{"welcome", true, "from " + env}))
			}
			r := one(t, ev, "welcome", evaluator.TypeString, nil, nil)
			require.Equal(t, "from ENV-A", r.Value)
			require.Equal(t, "ENV-A", r.FlagSet)
		})
	}
}

func orderEvaluator(t *testing.T) evaluator.Evaluator {
	t.Helper()
	ev, st := newEvaluator(t, nil, "src")
	deliver(t, st, "src", envDoc("ENV-B", false, feat{"zeta", true, 1}, feat{"alpha", true, 2}))
	deliver(t, st, "src", envDoc("ENV-A", false, feat{"mid", true, 3}, feat{"alpha", true, 4}))
	return ev
}

// TestEvaluateOrder pins the order of the results: a bulk request sorted by flag set, then key; explicit keys in
// request order, once per key asked, a repeated key twice.
func TestEvaluateOrder(t *testing.T) {
	ev := orderEvaluator(t)

	require.Equal(t, []string{"alpha@ENV-A", "mid@ENV-A", "alpha@ENV-B", "zeta@ENV-B"},
		keysAndSets(run(t, ev, evaluator.Request{Bulk: true})))

	keys := run(t, ev, evaluator.Request{FlagKeys: []string{"zeta", "missing", "alpha", "zeta"}})
	require.Equal(t, []string{"zeta@ENV-B", "missing@", "alpha@ENV-A", "zeta@ENV-B"}, keysAndSets(keys))
	require.Equal(t, model.FlagNotFoundErrorCode, keys[1].ErrorCode)

	restricted := run(t, ev, evaluator.Request{Bulk: true, FlagKeys: []string{"zeta", "missing"}})
	require.Equal(t, []string{"zeta@ENV-B", "missing@"}, keysAndSets(restricted),
		"a bulk request with keys answers per key, FLAG_NOT_FOUND included")
}

// TestEvaluateAppendsToDst pins that every path appends to dst and leaves what it held.
func TestEvaluateAppendsToDst(t *testing.T) {
	ev := orderEvaluator(t)
	prefix := evaluator.Result{FlagKey: "prefix", Format: "before"}
	for name, req := range map[string]evaluator.Request{
		"single":    {FlagKey: "alpha"},
		"not found": {FlagKey: "missing"},
		"keys":      {FlagKeys: []string{"zeta", "missing"}},
		"bulk":      {Bulk: true},
	} {
		t.Run(name, func(t *testing.T) {
			out, _, err := ev.Evaluate(context.Background(), req, []evaluator.Result{prefix})
			require.NoError(t, err)
			require.Greater(t, len(out), 1)
			require.Equal(t, prefix, out[0])
		})
	}
}

// TestEvaluateSkipsForeignDocuments pins that a document of the format whose value is not a decoded environment is
// skipped, not an error.
func TestEvaluateSkipsForeignDocuments(t *testing.T) {
	ev, st := newEvaluator(t, nil, "src", "other")
	deliver(t, st, "src", envDoc("ENV", false, feat{"welcome", true, "hello"}))
	require.NoError(t, st.Apply(store.ApplyRequest{Format: flagsmith.Type, Source: "other", Documents: []store.Document{
		{Format: flagsmith.Type, Source: "other", FlagSet: "ENV", FlagKeys: []string{"welcome"}, Value: "not a document"},
	}}))

	r := one(t, ev, "welcome", evaluator.TypeString, nil, nil)
	require.Equal(t, "hello", r.Value, "the higher-priority source holds no environment, so the other one answers")
	require.Equal(t, []string{"welcome@ENV"}, keysAndSets(run(t, ev, evaluator.Request{Bulk: true})))
}
