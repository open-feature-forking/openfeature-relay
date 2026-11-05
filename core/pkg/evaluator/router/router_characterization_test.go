package router

import (
	"context"
	"errors"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/stretchr/testify/require"
)

// resultKeys returns "key@flagSet" (or the key alone in the unnamed flag set) of each result, in order.
func resultKeys(results []evaluator.Result) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		key := r.FlagKey
		if r.FlagSet != "" {
			key += "@" + r.FlagSet
		}
		out = append(out, key)
	}
	return out
}

func newOrderRouter(t *testing.T) (*Router, *fake, *fake) {
	t.Helper()
	st := newStore(t, []string{"low", "high"},
		delivery{"a", "low", map[string][]string{"": {"z", "a"}, "x": {"m", "k"}, "y": {"k"}}},
		delivery{"b", "high", map[string][]string{"": {"b"}, "x": {"k"}}},
	)
	a := &fake{name: "a", flags: map[string][]string{"z": {""}, "a": {""}, "m": {"x"}, "k": {"x", "y"}}}
	b := &fake{name: "b", flags: map[string][]string{"b": {""}, "k": {"x"}}}
	r, err := New(st, Route{"a", a}, Route{"b", b})
	require.NoError(t, err)
	return r, a, b
}

// TestRouterResultOrder pins the order of the results of several evaluators, and which evaluator answers a key
// that two of them hold in different flag sets.
func TestRouterResultOrder(t *testing.T) {
	r, _, _ := newOrderRouter(t)
	ctx := context.Background()

	bulk, _, err := r.Evaluate(ctx, evaluator.Request{Bulk: true}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "z", "k@x", "m@x", "k@y"}, resultKeys(bulk), "sorted by flag set, then key")
	require.Equal(t, map[string]any{"a": "a", "b": "b", "z": "a", "k@x": "b", "m@x": "a", "k@y": "a"}, values(bulk),
		"the higher-priority source wins k in flag set x, the only holder wins it in y")

	explicit, _, err := r.Evaluate(ctx, evaluator.Request{FlagKeys: []string{"z", "missing", "a", "z"}}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"z", "missing", "a"}, resultKeys(explicit), "request order; a repeated key is answered once")
	require.Equal(t, model.FlagNotFoundErrorCode, explicit[1].ErrorCode)

	restricted, _, err := r.Evaluate(ctx, evaluator.Request{Bulk: true, FlagKeys: []string{"z", "missing", "a"}}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "z"}, resultKeys(restricted),
		"a bulk request with keys is restricted to them, sorted, with no FLAG_NOT_FOUND for a missing key")
}

// TestRouterAppendsToDst pins that every path appends to dst and leaves what it held.
func TestRouterAppendsToDst(t *testing.T) {
	prefix := evaluator.Result{FlagKey: "prefix", Format: "before"}
	check := func(t *testing.T, out []evaluator.Result) {
		t.Helper()
		require.NotEmpty(t, out)
		require.Equal(t, prefix, out[0])
	}
	ctx := context.Background()

	one, err := New(nil, Route{"a", &fake{name: "a", flags: map[string][]string{"k": {""}}}})
	require.NoError(t, err)
	out, _, err := one.Evaluate(ctx, evaluator.Request{FlagKey: "k"}, []evaluator.Result{prefix})
	require.NoError(t, err)
	check(t, out)
	require.Len(t, out, 2)

	r, _, _ := newOrderRouter(t)
	for name, req := range map[string]evaluator.Request{
		"single":    {FlagKey: "z"},
		"not found": {FlagKey: "missing"},
		"keys":      {FlagKeys: []string{"z", "missing"}},
		"bulk":      {Bulk: true},
	} {
		t.Run(name, func(t *testing.T) {
			out, _, err := r.Evaluate(ctx, req, []evaluator.Result{prefix})
			require.NoError(t, err)
			check(t, out)
			require.Greater(t, len(out), 1)
		})
	}

	t.Run("error", func(t *testing.T) {
		st := newStore(t, []string{"src", "other"},
			delivery{"a", "src", map[string][]string{"": {"k"}}},
			delivery{"b", "other", map[string][]string{"": {"b-key"}}},
		)
		failing, err := New(st, Route{"a", &fake{name: "a", err: errors.New("boom")}},
			Route{"b", &fake{name: "b", flags: map[string][]string{"b-key": {""}}}})
		require.NoError(t, err)
		out, _, err := failing.Evaluate(ctx, evaluator.Request{Bulk: true}, []evaluator.Result{prefix})
		require.NoError(t, err)
		check(t, out)
		require.Len(t, out, 3)
	})
}

// partial returns results and metadata together with an error.
type partial struct{ key string }

func (p partial) Evaluate(_ context.Context, _ evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	dst = append(dst, evaluator.Result{FlagKey: p.key, Value: "partial", HasValue: true})
	return dst, model.Metadata{"from-partial": true}, errors.New("boom")
}

// TestRouterRouteErrorDropsItsResults pins that the results and metadata of an evaluator that fails are dropped:
// its keys are GENERAL errors with the selector's metadata, and the request itself does not fail.
func TestRouterRouteErrorDropsItsResults(t *testing.T) {
	st := newStore(t, []string{"src", "other"},
		delivery{"a", "src", map[string][]string{"": {"k"}}},
		delivery{"b", "other", map[string][]string{"": {"b-key"}}},
	)
	b := &fake{name: "b", flags: map[string][]string{"b-key": {""}}, meta: model.Metadata{"from-b": true}}
	r, err := New(st, Route{"a", partial{key: "k"}}, Route{"b", b})
	require.NoError(t, err)

	sel := mustSelector(t, "flagSetId=")
	out, meta, err := r.Evaluate(context.Background(), evaluator.Request{Bulk: true, Selector: sel}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"b-key", "k"}, resultKeys(out))
	failed := out[1]
	require.Equal(t, evaluator.Result{
		FlagKey: "k", Reason: model.ErrorReason, ErrorCode: model.GeneralErrorCode, Format: "a",
		ErrorMessage: model.GetErrorMessage(model.GeneralErrorCode), Metadata: sel.ToMetadata(),
	}, failed)
	require.NotContains(t, meta, "from-partial", "the failing evaluator's metadata is dropped")
	require.Contains(t, meta, "from-b")
}

// TestRouterMetadataAliasing pins which results share one metadata map: the error and FLAG_NOT_FOUND results of a
// request share the selector's, the metadata a request of several evaluators returns is a copy of it, and a
// single key that no document holds returns the selector's map itself.
func TestRouterMetadataAliasing(t *testing.T) {
	st := newStore(t, []string{"src", "other"},
		delivery{"a", "src", map[string][]string{"": {"k"}}},
		delivery{"b", "other", map[string][]string{"": {"b-key"}}},
	)
	r, err := New(st, Route{"a", &fake{name: "a", err: errors.New("boom")}},
		Route{"b", &fake{name: "b", flags: map[string][]string{"b-key": {""}}}})
	require.NoError(t, err)
	sel := mustSelector(t, "flagSetId=")
	ctx := context.Background()

	out, meta, err := r.Evaluate(ctx, evaluator.Request{FlagKeys: []string{"k", "missing"}, Selector: sel}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"k", "missing"}, resultKeys(out))
	out[0].Metadata["probe"] = 1
	require.Contains(t, out[1].Metadata, "probe", "the GENERAL and the FLAG_NOT_FOUND result share one map")
	require.NotContains(t, meta, "probe", "the returned metadata is a copy")

	single, singleMeta, err := r.Evaluate(ctx, evaluator.Request{FlagKey: "missing", Selector: sel}, nil)
	require.NoError(t, err)
	require.Len(t, single, 1)
	single[0].Metadata["probe"] = 1
	require.Contains(t, singleMeta, "probe", "a single missing key returns the map of its result")
}

// recorder records the requests and whether dst was nil.
type recorder struct {
	reqs   []evaluator.Request
	nilDst []bool
}

func (r *recorder) Evaluate(_ context.Context, req evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	r.reqs = append(r.reqs, req)
	r.nilDst = append(r.nilDst, dst == nil)
	return dst, nil, nil
}

// TestRouterSubRequests pins what each evaluator is asked on the path of several evaluators: the sorted keys it
// won, no single key, the rest of the request as it came, and no dst.
func TestRouterSubRequests(t *testing.T) {
	st := newStore(t, []string{"low", "high"},
		delivery{"a", "low", map[string][]string{"": {"z", "a", "shared"}}},
		delivery{"b", "high", map[string][]string{"": {"shared", "b"}}},
	)
	a, b := &recorder{}, &recorder{}
	r, err := New(st, Route{"a", a}, Route{"b", b})
	require.NoError(t, err)
	evalCtx := map[string]any{"targetingKey": "u"}

	_, _, err = r.Evaluate(context.Background(), evaluator.Request{
		Bulk: true, FlagKey: "ignored", Context: evalCtx, TypeHint: evaluator.TypeBool,
	}, []evaluator.Result{{FlagKey: "prefix"}})
	require.NoError(t, err)

	require.Len(t, a.reqs, 1)
	require.Len(t, b.reqs, 1)
	require.Equal(t, []string{"a", "z"}, a.reqs[0].FlagKeys)
	require.Equal(t, []string{"b", "shared"}, b.reqs[0].FlagKeys)
	for _, req := range []evaluator.Request{a.reqs[0], b.reqs[0]} {
		require.Empty(t, req.FlagKey)
		require.True(t, req.Bulk)
		require.Equal(t, evalCtx, req.Context)
		require.Equal(t, evaluator.TypeBool, req.TypeHint)
	}
	require.Equal(t, []bool{true}, a.nilDst)
	require.Equal(t, []bool{true}, b.nilDst)
}
