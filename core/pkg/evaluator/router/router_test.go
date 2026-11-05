package router

import (
	"context"
	"errors"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"
)

// fake resolves the flags it was given for the flag sets the selector names, to its own name.
type fake struct {
	name  string
	flags map[string][]string // key -> flag sets
	calls []evaluator.Request
	err   error
	meta  model.Metadata
}

func (f *fake) Evaluate(_ context.Context, req evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return dst, nil, f.err
	}
	keys := req.FlagKeys
	if keys == nil {
		keys = []string{req.FlagKey}
	}
	flagSet, named := req.Selector.DocumentFlagSet()
	for _, key := range keys {
		sets, ok := f.flags[key]
		if !ok {
			dst = append(dst, evaluator.Result{FlagKey: key, ErrorCode: model.FlagNotFoundErrorCode, Reason: model.ErrorReason})
			continue
		}
		for _, set := range sets {
			if named && set != flagSet {
				continue
			}
			dst = append(dst, evaluator.Result{
				FlagKey: key, FlagSet: set, Value: f.name, HasValue: true, Reason: model.StaticReason,
				Metadata: model.Metadata{"flagSetId": set},
			})
			if !req.Bulk {
				break
			}
		}
	}
	return dst, f.meta, nil
}

// delivery is what one format holds for one source: flag set -> keys.
type delivery struct {
	format, source string
	sets           map[string][]string
}

func newStore(t *testing.T, sources []string, deliveries ...delivery) *store.Store {
	t.Helper()
	st, err := store.NewStore(logger.NewLogger(nil, false), sources)
	require.NoError(t, err)
	for _, d := range deliveries {
		var docs []store.Document
		for set, keys := range d.sets {
			docs = append(docs, store.Document{Format: d.format, Source: d.source, FlagSet: set, FlagKeys: keys})
		}
		require.NoError(t, st.Apply(store.ApplyRequest{Format: d.format, Source: d.source, Documents: docs}))
	}
	return st
}

func values(results []evaluator.Result) map[string]any {
	out := map[string]any{}
	for _, r := range results {
		key := r.FlagKey
		if r.FlagSet != "" {
			key += "@" + r.FlagSet
		}
		if r.HasValue {
			out[key] = r.Value
		} else {
			out[key] = r.ErrorCode
		}
	}
	return out
}

func TestRouterSourcePrecedence(t *testing.T) {
	st := newStore(t, []string{"low", "high"},
		delivery{"a", "low", map[string][]string{"": {"shared", "only-a"}}},
		delivery{"b", "high", map[string][]string{"": {"shared", "only-b"}}},
	)
	a := &fake{name: "a", flags: map[string][]string{"shared": {""}, "only-a": {""}}}
	b := &fake{name: "b", flags: map[string][]string{"shared": {""}, "only-b": {""}}}
	r, err := New(st, Route{"a", a}, Route{"b", b})
	require.NoError(t, err)

	single, _, err := r.Evaluate(context.Background(), evaluator.Request{FlagKey: "shared"}, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"shared": "b"}, values(single))

	bulk, _, err := r.Evaluate(context.Background(), evaluator.Request{Bulk: true}, nil)
	require.NoError(t, err)
	require.Len(t, bulk, 3)
	require.Equal(t, map[string]any{"shared": "b", "only-a": "a", "only-b": "b"}, values(bulk))

	// the lower-priority source wins when it is the only one the selector names
	sel, err := store.NewSelector("source=low")
	require.NoError(t, err)
	scoped, _, err := r.Evaluate(context.Background(), evaluator.Request{FlagKey: "shared", Selector: sel}, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"shared": "a"}, values(scoped))
}

func TestRouterSelectorScopesDocuments(t *testing.T) {
	st := newStore(t, []string{"low", "high"},
		delivery{"a", "low", map[string][]string{"x": {"k"}, "y": {"k"}}},
		delivery{"b", "high", map[string][]string{"x": {"k"}}},
	)
	a := &fake{name: "a", flags: map[string][]string{"k": {"x", "y"}}}
	b := &fake{name: "b", flags: map[string][]string{"k": {"x"}}}
	r, err := New(st, Route{"a", a}, Route{"b", b})
	require.NoError(t, err)

	// without a selector, a bulk request has one entry for each flag set holding the key
	bulk, _, err := r.Evaluate(context.Background(), evaluator.Request{Bulk: true}, nil)
	require.NoError(t, err)
	require.Len(t, bulk, 2)
	require.Equal(t, map[string]any{"k@x": "b", "k@y": "a"}, values(bulk))

	for selector, want := range map[string]map[string]any{
		"flagSetId=x": {"k@x": "b"},
		"flagSetId=y": {"k@y": "a"},
	} {
		sel, err := store.NewSelector(selector)
		require.NoError(t, err)
		got, _, err := r.Evaluate(context.Background(), evaluator.Request{Bulk: true, Selector: sel}, nil)
		require.NoError(t, err)
		require.Len(t, got, len(want), selector)
		require.Equal(t, want, values(got), selector)
	}

	none, _, err := r.Evaluate(context.Background(), evaluator.Request{FlagKey: "k", Selector: mustSelector(t, "flagSetId=z")}, nil)
	require.NoError(t, err)
	require.Equal(t, model.FlagNotFoundErrorCode, none[0].ErrorCode)
}

func mustSelector(t *testing.T, expression string) store.Selector {
	t.Helper()
	sel, err := store.NewSelector(expression)
	require.NoError(t, err)
	return sel
}

// The same key in two flag sets of one source is not the router's decision: the source's evaluator gets the
// request as it came, and a bulk request names the key once.
func TestRouterSameSourceFlagSetTie(t *testing.T) {
	st := newStore(t, []string{"src", "other"},
		delivery{"a", "src", map[string][]string{"x": {"k"}, "y": {"k"}}},
		delivery{"b", "other", map[string][]string{"": {"unrelated"}}},
	)
	a := &fake{name: "a", flags: map[string][]string{"k": {"x", "y"}}}
	b := &fake{name: "b", flags: map[string][]string{"unrelated": {""}}}
	r, err := New(st, Route{"a", a}, Route{"b", b})
	require.NoError(t, err)

	_, _, err = r.Evaluate(context.Background(), evaluator.Request{FlagKey: "k"}, nil)
	require.NoError(t, err)
	require.Len(t, a.calls, 1)
	require.Equal(t, "k", a.calls[0].FlagKey)
	require.Empty(t, b.calls)

	a.calls = nil
	bulk, _, err := r.Evaluate(context.Background(), evaluator.Request{Bulk: true}, nil)
	require.NoError(t, err)
	require.Len(t, a.calls, 1)
	require.Equal(t, []string{"k"}, a.calls[0].FlagKeys)
	require.True(t, a.calls[0].Bulk, "Bulk survives the split")
	require.Len(t, bulk, 3)
	require.Equal(t, map[string]any{"k@x": "a", "k@y": "a", "unrelated": "b"}, values(bulk))
}

func TestRouterUnknownKeysAndFormatsWithoutEvaluator(t *testing.T) {
	st := newStore(t, []string{"src", "other"},
		delivery{"a", "src", map[string][]string{"": {"k"}}},
		delivery{"c", "other", map[string][]string{"": {"orphan"}}},
		delivery{"b", "other", map[string][]string{"": {"b-key"}}},
	)
	a := &fake{name: "a", flags: map[string][]string{"k": {""}}}
	b := &fake{name: "b", flags: map[string][]string{"b-key": {""}}}
	r, err := New(st, Route{"a", a}, Route{"b", b})
	require.NoError(t, err)

	for _, key := range []string{"missing", "orphan"} {
		got, _, err := r.Evaluate(context.Background(), evaluator.Request{FlagKey: key}, nil)
		require.NoError(t, err)
		require.Equal(t, model.FlagNotFoundErrorCode, got[0].ErrorCode, key)
	}

	bulk, _, err := r.Evaluate(context.Background(), evaluator.Request{Bulk: true}, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"k": "a", "b-key": "b"}, values(bulk), "the format without an evaluator contributes nothing")

	listed, _, err := r.Evaluate(context.Background(), evaluator.Request{FlagKeys: []string{"orphan", "k"}}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"orphan", "k"}, []string{listed[0].FlagKey, listed[1].FlagKey})
	require.Equal(t, model.FlagNotFoundErrorCode, listed[0].ErrorCode)
}

func TestRouterEvaluatorErrorIsGeneralForItsKeys(t *testing.T) {
	st := newStore(t, []string{"src", "other"},
		delivery{"a", "src", map[string][]string{"": {"k"}}},
		delivery{"b", "other", map[string][]string{"": {"b-key"}}},
	)
	a := &fake{name: "a", err: errors.New("boom")}
	b := &fake{name: "b", flags: map[string][]string{"b-key": {""}}}
	r, err := New(st, Route{"a", a}, Route{"b", b})
	require.NoError(t, err)

	bulk, _, err := r.Evaluate(context.Background(), evaluator.Request{Bulk: true}, nil)
	require.NoError(t, err)
	require.Len(t, bulk, 2)
	require.Equal(t, map[string]any{"k": model.GeneralErrorCode, "b-key": "b"}, values(bulk))
}

func TestRouterSingleEvaluatorFastPath(t *testing.T) {
	a := &fake{name: "a", flags: map[string][]string{"k": {""}}}
	r, err := New(nil, Route{"a", a})
	require.NoError(t, err)

	ctx, req := context.Background(), evaluator.Request{FlagKey: "k"}
	buffer := make([]evaluator.Result, 0, 1)
	direct := testing.AllocsPerRun(100, func() {
		a.calls = a.calls[:0]
		_, _, _ = a.Evaluate(ctx, req, buffer[:0])
	})
	routed := testing.AllocsPerRun(100, func() {
		a.calls = a.calls[:0]
		_, _, _ = r.Evaluate(ctx, req, buffer[:0])
	})
	require.Equal(t, direct, routed)
}

// TestRouterStampsFormat pins that every result carries the format of the evaluator that resolved it, on the
// single-evaluator fast path and on every path of several evaluators.
func TestRouterStampsFormat(t *testing.T) {
	t.Run("fast path", func(t *testing.T) {
		a := &fake{name: "a", flags: map[string][]string{"k": {""}}}
		r, err := New(nil, Route{"a", a})
		require.NoError(t, err)

		out, _, err := r.Evaluate(context.Background(), evaluator.Request{FlagKey: "k"}, nil)
		require.NoError(t, err)
		require.Equal(t, "a", out[0].Format)
	})

	t.Run("several evaluators", func(t *testing.T) {
		st := newStore(t, []string{"low", "high"},
			delivery{"a", "low", map[string][]string{"": {"shared", "only-a"}}},
			delivery{"b", "high", map[string][]string{"": {"shared", "only-b"}}},
		)
		a := &fake{name: "a", flags: map[string][]string{"shared": {""}, "only-a": {""}}}
		b := &fake{name: "b", flags: map[string][]string{"shared": {""}, "only-b": {""}}}
		r, err := New(st, Route{"a", a}, Route{"b", b})
		require.NoError(t, err)

		single, _, err := r.Evaluate(context.Background(), evaluator.Request{FlagKey: "shared"}, nil)
		require.NoError(t, err)
		require.Equal(t, "b", single[0].Format, "the evaluator of the higher-priority source")

		bulk, _, err := r.Evaluate(context.Background(), evaluator.Request{Bulk: true}, nil)
		require.NoError(t, err)
		formats := map[string]string{}
		for _, res := range bulk {
			formats[res.FlagKey] = res.Format
		}
		require.Equal(t, map[string]string{"shared": "b", "only-a": "a", "only-b": "b"}, formats)

		missing, _, err := r.Evaluate(context.Background(), evaluator.Request{FlagKey: "none"}, nil)
		require.NoError(t, err)
		require.Empty(t, missing[0].Format, "no format resolved a flag no document holds")
	})
}

func TestRouterEvaluatorByFormat(t *testing.T) {
	a, b := &fake{name: "a"}, &fake{name: "b"}
	st := newStore(t, []string{"s"})
	r, err := New(st, Route{"a", a}, Route{"b", b})
	require.NoError(t, err)

	got, ok := r.Evaluator("b")
	require.True(t, ok)
	require.Same(t, b, got)
	_, ok = r.Evaluator("c")
	require.False(t, ok)
}

func TestNewRejectsBadRoutes(t *testing.T) {
	a := &fake{}
	_, err := New(nil)
	require.Error(t, err)
	_, err = New(nil, Route{"", a})
	require.Error(t, err)
	_, err = New(nil, Route{"a", nil})
	require.Error(t, err)
	st := newStore(t, []string{"src"})
	_, err = New(st, Route{"a", a}, Route{"a", a})
	require.ErrorContains(t, err, "two evaluators")
	_, err = New(nil, Route{"a", a}, Route{"b", a})
	require.Error(t, err, "several evaluators need a store")
}

func TestRouterMergesTheEvaluatorsMetadata(t *testing.T) {
	st := newStore(t, []string{"src", "other"},
		delivery{"a", "src", map[string][]string{"": {"k"}}},
		delivery{"b", "other", map[string][]string{"": {"b-key"}}},
	)
	a := &fake{name: "a", flags: map[string][]string{"k": {""}}, meta: model.Metadata{"scope": "a", "shared": "a"}}
	b := &fake{name: "b", flags: map[string][]string{"b-key": {""}}, meta: model.Metadata{"extra": "b", "shared": "b"}}
	r, err := New(st, Route{"a", a}, Route{"b", b})
	require.NoError(t, err)

	_, meta, err := r.Evaluate(context.Background(), evaluator.Request{Bulk: true, Selector: mustSelector(t, "flagSetId=")}, nil)
	require.NoError(t, err)
	require.Equal(t, "a", meta["scope"])
	require.Equal(t, "b", meta["extra"])
	require.Equal(t, "a", meta["shared"], "the first route's value stands")
	require.Contains(t, meta, "flagSetId", "the selector's metadata stays")
}

// stub answers from results it built once, so that a benchmark shows the router's own allocations: it returns
// them for a sub-request, which has no dst, and appends one to a dst that has room.
type stub struct{ results []evaluator.Result }

func (s stub) Evaluate(_ context.Context, _ evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	if dst == nil {
		return s.results, nil, nil
	}
	return append(dst, s.results[0]), nil, nil
}

func stubOf(keys ...string) stub {
	s := stub{}
	for _, key := range keys {
		s.results = append(s.results, evaluator.Result{FlagKey: key, Value: true, HasValue: true, Reason: model.StaticReason})
	}
	return s
}

func BenchmarkRouterEvaluate(b *testing.B) {
	st, err := store.NewStore(logger.NewLogger(nil, false), []string{"low", "high"})
	require.NoError(b, err)
	require.NoError(b, st.Apply(store.ApplyRequest{Format: "a", Source: "low", Documents: []store.Document{
		{Format: "a", Source: "low", FlagKeys: []string{"a1", "a2", "a3", "shared"}},
	}}))
	require.NoError(b, st.Apply(store.ApplyRequest{Format: "b", Source: "high", Documents: []store.Document{
		{Format: "b", Source: "high", FlagKeys: []string{"b1", "b2", "shared"}},
	}}))
	one, err := New(nil, Route{"a", stubOf("a1")})
	require.NoError(b, err)
	two, err := New(st, Route{"a", stubOf("a1", "a2", "a3")}, Route{"b", stubOf("b1", "b2", "shared")})
	require.NoError(b, err)
	source, err := store.NewSelector("source=low")
	require.NoError(b, err)

	for _, bc := range []struct {
		name   string
		router *Router
		req    evaluator.Request
	}{
		{"one-route", one, evaluator.Request{FlagKey: "a1"}},
		{"two-routes/single", two, evaluator.Request{FlagKey: "a1"}},
		{"two-routes/bulk", two, evaluator.Request{Bulk: true}},
		{"two-routes/keys", two, evaluator.Request{FlagKeys: []string{"a1", "b1", "shared", "missing"}}},
		{"two-routes/selector", two, evaluator.Request{Bulk: true, Selector: source}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			ctx, dst := context.Background(), make([]evaluator.Result, 0, 16)
			b.ReportAllocs()
			for b.Loop() {
				_, _, _ = bc.router.Evaluate(ctx, bc.req, dst[:0])
			}
		})
	}
}
