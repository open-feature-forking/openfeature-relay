package typed

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"
)

// fake answers every request with fixed results and records the last request.
type fake struct {
	results  []evaluator.Result
	metadata model.Metadata
	err      error
	last     evaluator.Request
}

func (f *fake) Evaluate(_ context.Context, req evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	f.last = req
	return append(dst, f.results...), f.metadata, f.err
}

func one(r evaluator.Result) *fake {
	r.FlagKey = "k"
	return &fake{results: []evaluator.Result{r}}
}

func static(value any) evaluator.Result {
	return evaluator.Result{Value: value, HasValue: true, Variant: "v", Reason: model.StaticReason}
}

func TestSingleCallsSetKeyHintAndRequestFields(t *testing.T) {
	f := one(static(true))
	selector, err := store.NewSelector("source=s")
	require.NoError(t, err)
	ctx := context.WithValue(context.Background(), store.SelectorContextKey{}, selector)
	ctx = evaluator.WithAttributes(ctx, map[string]any{"audience": "client"})
	evalCtx := map[string]any{"email": "a@b.c"}

	calls := []struct {
		hint evaluator.Type
		call func(a evaluator.IResolver)
	}{
		{evaluator.TypeBool, func(a evaluator.IResolver) { _, _, _, _, _ = a.ResolveBooleanValue(ctx, "id", "k", evalCtx) }},
		{evaluator.TypeString, func(a evaluator.IResolver) { _, _, _, _, _ = a.ResolveStringValue(ctx, "id", "k", evalCtx) }},
		{evaluator.TypeInt, func(a evaluator.IResolver) { _, _, _, _, _ = a.ResolveIntValue(ctx, "id", "k", evalCtx) }},
		{evaluator.TypeFloat, func(a evaluator.IResolver) { _, _, _, _, _ = a.ResolveFloatValue(ctx, "id", "k", evalCtx) }},
		{evaluator.TypeObject, func(a evaluator.IResolver) { _, _, _, _, _ = a.ResolveObjectValue(ctx, "id", "k", evalCtx) }},
		{evaluator.TypeAny, func(a evaluator.IResolver) { _ = a.ResolveAsAnyValue(ctx, "id", "k", evalCtx) }},
	}
	for _, c := range calls {
		c.call(New(f))
		require.Equal(t, "k", f.last.FlagKey)
		require.Nil(t, f.last.FlagKeys)
		require.False(t, f.last.Bulk)
		require.Equal(t, c.hint, f.last.TypeHint)
		require.Equal(t, selector, f.last.Selector)
		require.Equal(t, evalCtx, f.last.Context)
		require.Equal(t, map[string]any{"audience": "client"}, f.last.Attributes)
	}
}

func TestResolveByType(t *testing.T) {
	object := map[string]any{"k": 1.0}
	tests := map[string]struct {
		result evaluator.Result
		call   func(a evaluator.IResolver) (any, string, string, error)
		want   any
		err    string
	}{
		"bool": {static(true), func(a evaluator.IResolver) (any, string, string, error) {
			v, variant, reason, _, err := a.ResolveBooleanValue(context.Background(), "", "k", nil)
			return v, variant, reason, err
		}, true, ""},
		"bool mismatch": {static("x"), func(a evaluator.IResolver) (any, string, string, error) {
			v, variant, reason, _, err := a.ResolveBooleanValue(context.Background(), "", "k", nil)
			return v, variant, reason, err
		}, false, model.TypeMismatchErrorCode},
		"string": {static("x"), func(a evaluator.IResolver) (any, string, string, error) {
			v, variant, reason, _, err := a.ResolveStringValue(context.Background(), "", "k", nil)
			return v, variant, reason, err
		}, "x", ""},
		"string mismatch": {static(1.0), func(a evaluator.IResolver) (any, string, string, error) {
			v, variant, reason, _, err := a.ResolveStringValue(context.Background(), "", "k", nil)
			return v, variant, reason, err
		}, "", model.TypeMismatchErrorCode},
		"object": {static(object), func(a evaluator.IResolver) (any, string, string, error) {
			v, variant, reason, _, err := a.ResolveObjectValue(context.Background(), "", "k", nil)
			return v, variant, reason, err
		}, object, ""},
		"object mismatch": {static(true), func(a evaluator.IResolver) (any, string, string, error) {
			v, variant, reason, _, err := a.ResolveObjectValue(context.Background(), "", "k", nil)
			return v, variant, reason, err
		}, map[string]any(nil), model.TypeMismatchErrorCode},
		"float": {static(0.5), func(a evaluator.IResolver) (any, string, string, error) {
			v, variant, reason, _, err := a.ResolveFloatValue(context.Background(), "", "k", nil)
			return v, variant, reason, err
		}, 0.5, ""},
		"float from int64": {static(int64(3)), func(a evaluator.IResolver) (any, string, string, error) {
			v, variant, reason, _, err := a.ResolveFloatValue(context.Background(), "", "k", nil)
			return v, variant, reason, err
		}, 3.0, ""},
		"float mismatch": {static("x"), func(a evaluator.IResolver) (any, string, string, error) {
			v, variant, reason, _, err := a.ResolveFloatValue(context.Background(), "", "k", nil)
			return v, variant, reason, err
		}, 0.0, model.TypeMismatchErrorCode},
		"int": {static(10.0), func(a evaluator.IResolver) (any, string, string, error) {
			v, variant, reason, _, err := a.ResolveIntValue(context.Background(), "", "k", nil)
			return v, variant, reason, err
		}, int64(10), ""},
		"int from int64": {static(int64(1) << 60), func(a evaluator.IResolver) (any, string, string, error) {
			v, variant, reason, _, err := a.ResolveIntValue(context.Background(), "", "k", nil)
			return v, variant, reason, err
		}, int64(1) << 60, ""},
		"int fractional": {static(0.5), func(a evaluator.IResolver) (any, string, string, error) {
			v, variant, reason, _, err := a.ResolveIntValue(context.Background(), "", "k", nil)
			return v, variant, reason, err
		}, int64(0), model.TypeMismatchErrorCode},
		"int outside int64": {static(1e19), func(a evaluator.IResolver) (any, string, string, error) {
			v, variant, reason, _, err := a.ResolveIntValue(context.Background(), "", "k", nil)
			return v, variant, reason, err
		}, int64(0), model.TypeMismatchErrorCode},
		"int mismatch": {static("x"), func(a evaluator.IResolver) (any, string, string, error) {
			v, variant, reason, _, err := a.ResolveIntValue(context.Background(), "", "k", nil)
			return v, variant, reason, err
		}, int64(0), model.TypeMismatchErrorCode},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, variant, reason, err := tt.call(New(one(tt.result)))
			require.Equal(t, tt.want, got)
			require.Equal(t, "v", variant)
			if tt.err == "" {
				require.NoError(t, err)
				require.Equal(t, model.StaticReason, reason)
				return
			}
			require.EqualError(t, err, tt.err)
			require.Equal(t, model.ErrorReason, reason)
		})
	}
}

func TestStrictNumericRange(t *testing.T) {
	big := math.Pow(2, 53) // 2^53 is outside [-(2^53-1), 2^53-1]

	lenient := New(one(static(big)))
	strict := New(one(static(big)), WithStrictNumericRange())

	i, _, _, _, err := lenient.ResolveIntValue(context.Background(), "", "k", nil)
	require.NoError(t, err)
	require.Equal(t, int64(big), i)
	f, _, _, _, err := lenient.ResolveFloatValue(context.Background(), "", "k", nil)
	require.NoError(t, err)
	require.Equal(t, big, f)

	_, _, reason, _, err := strict.ResolveIntValue(context.Background(), "", "k", nil)
	require.EqualError(t, err, model.TypeMismatchErrorCode)
	require.Equal(t, model.ErrorReason, reason)
	_, _, _, _, err = strict.ResolveFloatValue(context.Background(), "", "k", nil)
	require.EqualError(t, err, model.TypeMismatchErrorCode)

	// the largest safe integer passes, and strict mode rejects fractions only for integers
	safe := New(one(static(float64(maxSafeInteger))), WithStrictNumericRange())
	i, _, _, _, err = safe.ResolveIntValue(context.Background(), "", "k", nil)
	require.NoError(t, err)
	require.Equal(t, int64(maxSafeInteger), i)
	_, _, _, _, err = New(one(static(0.5)), WithStrictNumericRange()).ResolveIntValue(context.Background(), "", "k", nil)
	require.EqualError(t, err, model.TypeMismatchErrorCode)
	_, _, _, _, err = New(one(static(0.5)), WithStrictNumericRange()).ResolveFloatValue(context.Background(), "", "k", nil)
	require.NoError(t, err)
}

func TestAbsentValueFallsBackToCallerDefault(t *testing.T) {
	absent := evaluator.Result{Reason: model.DefaultReason, Metadata: model.Metadata{"m": 1}}
	a := New(one(absent))

	b, variant, reason, metadata, err := a.ResolveBooleanValue(context.Background(), "", "k", nil)
	require.NoError(t, err)
	require.False(t, b)
	require.Empty(t, variant)
	require.Equal(t, model.FallbackReason, reason, "the adapter restores FALLBACK toward the endpoints")
	require.Equal(t, model.Metadata{"m": 1}, metadata)

	any := a.ResolveAsAnyValue(context.Background(), "", "k", nil)
	require.Nil(t, any.Value)
	require.Equal(t, model.FallbackReason, any.Reason)
	require.NoError(t, any.Error)
	require.Equal(t, "k", any.FlagKey)
}

func TestSingleCallLegacyRuleForAbsentDefault(t *testing.T) {
	ctx := context.WithValue(context.Background(), evaluator.ProtoVersionKey, "v1") //nolint:staticcheck // exported string key
	a := New(one(evaluator.Result{Reason: model.DefaultReason}))

	_, variant, reason, _, err := a.ResolveStringValue(ctx, "", "k", nil)
	require.EqualError(t, err, model.FlagNotFoundErrorCode)
	require.Equal(t, model.ErrorReason, reason)
	require.Empty(t, variant)

	// a disabled flag is not an absent default
	_, _, reason, _, err = New(one(evaluator.Result{Reason: model.DisabledReason})).ResolveStringValue(ctx, "", "k", nil)
	require.NoError(t, err)
	require.Equal(t, model.DisabledReason, reason)
}

func TestDisabled(t *testing.T) {
	a := New(one(evaluator.Result{Reason: model.DisabledReason, Variant: "ignored"}))
	v, variant, reason, _, err := a.ResolveIntValue(context.Background(), "", "k", nil)
	require.NoError(t, err)
	require.Zero(t, v)
	require.Empty(t, variant)
	require.Equal(t, model.DisabledReason, reason)
}

func TestErrorCodesBecomeErrors(t *testing.T) {
	notFound := New(one(evaluator.Result{ErrorCode: model.FlagNotFoundErrorCode, Reason: model.ErrorReason, ErrorMessage: "detail"}))
	_, _, reason, _, err := notFound.ResolveBooleanValue(context.Background(), "", "k", nil)
	require.EqualError(t, err, model.FlagNotFoundErrorCode)
	require.Equal(t, model.ErrorReason, reason)

	any := notFound.ResolveAsAnyValue(context.Background(), "", "k", nil)
	require.EqualError(t, any.Error, model.FlagNotFoundErrorCode)

	whole := New(&fake{err: errors.New("store down")})
	_, _, reason, _, err = whole.ResolveBooleanValue(context.Background(), "", "k", nil)
	require.EqualError(t, err, "store down")
	require.Equal(t, model.ErrorReason, reason)

	empty := New(&fake{})
	_, _, _, _, err = empty.ResolveStringValue(context.Background(), "", "k", nil)
	require.EqualError(t, err, model.FlagNotFoundErrorCode)
}

func TestResolveAllValues(t *testing.T) {
	f := &fake{
		metadata: model.Metadata{"scope": "s"},
		results: []evaluator.Result{
			{FlagKey: "a", Value: true, HasValue: true, Variant: "on", Reason: model.StaticReason},
			{FlagKey: "b", Value: false, Reason: model.DefaultReason, Variant: "dropped"},
			{FlagKey: "c", Reason: model.DisabledReason, Variant: "dropped"},
			{FlagKey: "d", ErrorCode: model.TypeMismatchErrorCode, Reason: model.ErrorReason, Variant: "x"},
		},
	}
	selector, err := store.NewSelector("flagSetId=s")
	require.NoError(t, err)
	ctx := context.WithValue(context.Background(), store.SelectorContextKey{}, selector)

	values, metadata, err := New(f).ResolveAllValues(ctx, "id", map[string]any{"e": 1})
	require.NoError(t, err)
	require.Equal(t, model.Metadata{"scope": "s"}, metadata)
	require.True(t, f.last.Bulk)
	require.Empty(t, f.last.FlagKey)
	require.Equal(t, selector, f.last.Selector)
	require.Equal(t, map[string]any{"e": 1}, f.last.Context)

	require.Len(t, values, 4)
	require.Equal(t, evaluator.AnyValue{Value: true, Variant: "on", Reason: model.StaticReason, FlagKey: "a"}, values[0])
	require.Equal(t, evaluator.AnyValue{Value: false, Reason: model.FallbackReason, FlagKey: "b"}, values[1], "the evaluator's typed zero is kept")
	require.Equal(t, evaluator.AnyValue{Reason: model.DisabledReason, FlagKey: "c"}, values[2])
	require.Equal(t, "d", values[3].FlagKey)
	require.Equal(t, model.ErrorReason, values[3].Reason)
	require.EqualError(t, values[3].Error, model.TypeMismatchErrorCode)

	_, _, err = New(&fake{err: errors.New("down")}).ResolveAllValues(context.Background(), "id", nil)
	require.EqualError(t, err, "down")
}

func TestSingleCallDoesNotAllocate(t *testing.T) {
	a := New(one(static(true)))
	ctx := context.Background()
	allocs := testing.AllocsPerRun(100, func() {
		_, _, _, _, _ = a.ResolveBooleanValue(ctx, "id", "k", nil)
	})
	require.Zero(t, allocs, "a single-flag call adds no allocation of its own")
}

func TestResultsAreNotRetainedByThePool(t *testing.T) {
	f := one(static(map[string]any{"k": 1}))
	a := New(f)
	_, _, _, _, err := a.ResolveObjectValue(context.Background(), "", "k", nil)
	require.NoError(t, err)

	var seen []evaluator.Result
	g := &grabber{}
	b := New(g)
	_, _, _, _, _ = b.ResolveObjectValue(context.Background(), "", "k", nil)
	seen = g.dst
	require.Empty(t, seen, "buffers handed to an evaluator start empty")
}

type grabber struct{ dst []evaluator.Result }

func (g *grabber) Evaluate(_ context.Context, _ evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	g.dst = dst
	return append(dst, static(map[string]any{})), nil, nil
}

func TestObserverSeesTheFinalResult(t *testing.T) {
	var got []evaluator.Result
	var req evaluator.Request
	a := New(one(static(3.0)), WithObserver(func(_ context.Context, r evaluator.Request, res evaluator.Result) {
		req = r
		got = append(got, res)
	}))

	value, _, _, _, err := a.ResolveIntValue(context.Background(), "", "k", nil)
	require.NoError(t, err)
	require.Equal(t, int64(3), value)
	require.Equal(t, "k", req.FlagKey)
	require.Equal(t, evaluator.TypeInt, req.TypeHint)
	require.Equal(t, []evaluator.Result{{FlagKey: "k", Value: int64(3), HasValue: true, Variant: "v", Reason: model.StaticReason}}, got)
}

func TestObserverIsCalledOncePerBulkFlag(t *testing.T) {
	f := &fake{results: []evaluator.Result{
		{FlagKey: "a", Value: true, HasValue: true, Format: "flagd"},
		{FlagKey: "b", ErrorCode: model.FlagNotFoundErrorCode},
	}}
	var keys []string
	var bulk bool
	a := New(f, WithObserver(func(_ context.Context, r evaluator.Request, res evaluator.Result) {
		bulk = r.Bulk
		keys = append(keys, res.FlagKey)
	}))

	_, _, err := a.ResolveAllValues(context.Background(), "", nil)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, keys)
	require.True(t, bulk)
}
