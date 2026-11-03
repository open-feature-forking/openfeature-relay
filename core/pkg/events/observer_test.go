package events

import (
	"context"
	"errors"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/typed"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/stretchr/testify/require"
)

// answering returns the results as the evaluator's answer to every request.
type answering struct {
	results []evaluator.Result
	err     error
}

func (a answering) Evaluate(_ context.Context, _ evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	return append(dst, a.results...), nil, a.err
}

func observed(t *testing.T, ev evaluator.Evaluator) (evaluator.IResolver, *Subscription) {
	t.Helper()
	bus := NewBus()
	sub := bus.Subscribe(SubscribeOptions{Kinds: []Kind{KindEvaluation}, Buffer: 16})
	t.Cleanup(sub.Close)
	return typed.New(ev, typed.WithObserver(EvaluationObserver(bus, "test-endpoint"))), sub
}

func received(sub *Subscription) []Evaluation {
	var out []Evaluation
	for {
		select {
		case e := <-sub.C():
			out = append(out, e.(Evaluation))
		default:
			return out
		}
	}
}

func result(key string, value any) evaluator.Result {
	return evaluator.Result{
		FlagKey: key, Value: value, HasValue: true, Variant: "on", Reason: model.StaticReason, Format: "flagd",
		Metadata: model.Metadata{"flagSetId": "fs"},
	}
}

// TestObserverOncePerFlag pins the events the typed adapter's observer publishes: one per flag, with the
// result the adapter returns, not the evaluator's.
func TestObserverOncePerFlag(t *testing.T) {
	ctx := context.Background()

	t.Run("single", func(t *testing.T) {
		a, sub := observed(t, answering{results: []evaluator.Result{result("k", true)}})
		_, _, _, _, err := a.ResolveBooleanValue(ctx, "", "k", map[string]any{"targetingKey": "user-1"})
		require.NoError(t, err)
		got := received(sub)
		require.Len(t, got, 1)
		require.False(t, got[0].Time.IsZero())
		require.Equal(t, Evaluation{
			Time: got[0].Time, FlagKey: "k", FlagSet: "fs", Format: "flagd", Endpoint: "test-endpoint", Variant: "on",
			Reason: model.StaticReason, TargetingKey: "user-1", Value: true, HasValue: true, Origin: OriginService,
		}, got[0])
	})

	t.Run("type mismatch is reported as the caller sees it", func(t *testing.T) {
		a, sub := observed(t, answering{results: []evaluator.Result{result("k", "text")}})
		_, _, _, _, err := a.ResolveBooleanValue(ctx, "", "k", nil)
		require.EqualError(t, err, model.TypeMismatchErrorCode)
		got := received(sub)
		require.Len(t, got, 1)
		require.Equal(t, model.TypeMismatchErrorCode, got[0].ErrorCode)
		require.Equal(t, model.ErrorReason, got[0].Reason)
		require.False(t, got[0].HasValue)
		require.Nil(t, got[0].Value)
	})

	t.Run("an int64 converted from a float", func(t *testing.T) {
		a, sub := observed(t, answering{results: []evaluator.Result{result("k", 3.0)}})
		value, _, _, _, err := a.ResolveIntValue(ctx, "", "k", nil)
		require.NoError(t, err)
		require.Equal(t, int64(3), value)
		got := received(sub)
		require.Len(t, got, 1)
		require.Equal(t, int64(3), got[0].Value, "the converted value")
		require.True(t, got[0].HasValue)
	})

	t.Run("an absent value is the caller's default", func(t *testing.T) {
		absent := evaluator.Result{FlagKey: "k", Reason: model.DefaultReason, Format: "flagd"}
		a, sub := observed(t, answering{results: []evaluator.Result{absent}})
		_, _, reason, _, err := a.ResolveStringValue(ctx, "", "k", nil)
		require.NoError(t, err)
		require.Equal(t, model.FallbackReason, reason)
		got := received(sub)
		require.Len(t, got, 1)
		require.False(t, got[0].HasValue, "the caller's default is not a value")
		require.Nil(t, got[0].Value)
		require.Equal(t, model.FallbackReason, got[0].Reason)
	})

	t.Run("an evaluator error", func(t *testing.T) {
		a, sub := observed(t, answering{err: errors.New("down")})
		_, _, _, _, err := a.ResolveBooleanValue(ctx, "", "k", nil)
		require.Error(t, err)
		got := received(sub)
		require.Len(t, got, 1)
		require.Equal(t, model.GeneralErrorCode, got[0].ErrorCode, "an error that is no code is GENERAL")
		require.Equal(t, "k", got[0].FlagKey)
	})

	t.Run("any value", func(t *testing.T) {
		a, sub := observed(t, answering{results: []evaluator.Result{result("k", 1.5)}})
		a.ResolveAsAnyValue(ctx, "", "k", nil)
		got := received(sub)
		require.Len(t, got, 1)
		require.Equal(t, 1.5, got[0].Value)
	})

	t.Run("bulk", func(t *testing.T) {
		missing := evaluator.Result{FlagKey: "b", ErrorCode: model.FlagNotFoundErrorCode, Format: "flagd"}
		off := evaluator.Result{FlagKey: "c", Reason: model.DisabledReason, Format: "flagd"}
		a, sub := observed(t, answering{results: []evaluator.Result{result("a", true), missing, off}})
		values, _, err := a.ResolveAllValues(ctx, "", nil)
		require.NoError(t, err)
		require.Len(t, values, 3)
		got := received(sub)
		require.Len(t, got, 3, "one event per flag")
		require.Equal(t, "a", got[0].FlagKey)
		require.True(t, got[0].HasValue)
		require.Equal(t, model.FlagNotFoundErrorCode, got[1].ErrorCode)
		require.Equal(t, model.ErrorReason, got[1].Reason)
		require.False(t, got[2].HasValue)
		require.Equal(t, model.DisabledReason, got[2].Reason)
	})
}

func TestObserverNoSubscriberAllocs(t *testing.T) {
	bus := NewBus()
	observer := EvaluationObserver(bus, "e")
	req := evaluator.Request{Context: map[string]any{"targetingKey": "u"}}
	res := evaluator.Result{FlagKey: "k", Value: "v", HasValue: true}

	require.Zero(t, testing.AllocsPerRun(100, func() { observer(context.Background(), req, res) }))

	a := typed.New(answering{results: []evaluator.Result{result("k", true)}}, typed.WithObserver(observer))
	base := typed.New(answering{results: []evaluator.Result{result("k", true)}})
	call := func(r evaluator.IResolver) float64 {
		return testing.AllocsPerRun(100, func() { _, _, _, _, _ = r.ResolveBooleanValue(context.Background(), "", "k", nil) })
	}
	require.Equal(t, call(base), call(a), "a typed call with an observer and no subscriber adds no allocation")
}
