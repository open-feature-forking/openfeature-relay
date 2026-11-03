package flagd

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/evaluatortest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/typed"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	flagsync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

func newEvaluatorWith(t testing.TB, flags string, opts ...JSONEvaluatorOption) (*JSON, *store.Store) {
	t.Helper()
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"src"})
	require.NoError(t, err)
	ev := NewJSON(log, st, opts...)
	require.NoError(t, ev.SetState(flagsync.DataSync{Source: "src", FlagData: flags}))
	return ev, st
}

const evaluateFlags = `{"flags": {
  "static-bool": {"state": "ENABLED", "defaultVariant": "on", "variants": {"on": true, "off": false}},
  "targeted": {"state": "ENABLED", "defaultVariant": "off", "variants": {"on": true, "off": false},
    "targeting": {"if": [{"==": [{"var": "email"}, "a@b.c"]}, "on", "off"]}},
  "huge": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": 9007199254740993}},
  "fraction": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": 0.5}},
  "text": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": "s", "n": 1}}
}}`

// The typed methods are the typed adapter over Evaluate: an adapter built from the evaluator and the same options
// resolves every flag as they do.
func TestTypedAdapterMatchesCharacterization(t *testing.T) {
	for _, strict := range []bool{false, true} {
		var opts []JSONEvaluatorOption
		var adapterOpts []typed.Option
		if strict {
			opts = append(opts, WithStrictNumericRange())
			adapterOpts = append(adapterOpts, typed.WithStrictNumericRange())
		}
		ev, _ := newEvaluatorWith(t, evaluateFlags, opts...)
		adapter := typed.New(ev, adapterOpts...)
		ctx := context.Background()

		for _, key := range []string{"static-bool", "targeted", "huge", "fraction", "text", "missing"} {
			for _, evalCtx := range []map[string]any{nil, {"email": "a@b.c"}} {
				name := fmt.Sprintf("strict=%v/%s/%v", strict, key, evalCtx)
				b1, v1, r1, m1, e1 := ev.ResolveBooleanValue(ctx, "", key, evalCtx)
				b2, v2, r2, m2, e2 := adapter.ResolveBooleanValue(ctx, "", key, evalCtx)
				require.Equal(t, []any{b1, v1, r1, m1, e1}, []any{b2, v2, r2, m2, e2}, name)

				i1, v1, r1, m1, e1 := ev.ResolveIntValue(ctx, "", key, evalCtx)
				i2, v2, r2, m2, e2 := adapter.ResolveIntValue(ctx, "", key, evalCtx)
				require.Equal(t, []any{i1, v1, r1, m1, e1}, []any{i2, v2, r2, m2, e2}, name)

				f1, v1, r1, m1, e1 := ev.ResolveFloatValue(ctx, "", key, evalCtx)
				f2, v2, r2, m2, e2 := adapter.ResolveFloatValue(ctx, "", key, evalCtx)
				require.Equal(t, []any{f1, v1, r1, m1, e1}, []any{f2, v2, r2, m2, e2}, name)

				require.Equal(t, ev.ResolveAsAnyValue(ctx, "", key, evalCtx), adapter.ResolveAsAnyValue(ctx, "", key, evalCtx), name)
			}
		}
	}
}

// staticBoolAllocs is the allocations of resolving a static boolean flag before the adapter existed:
// BenchmarkResolveBooleanValue/test_staticBoolFlag at the branch point.
const staticBoolAllocs = 22

// The adapter over Evaluate on a real store adds no allocation to a single-flag resolution (NFR-001).
func TestAdapterAllocs(t *testing.T) {
	ev, _ := newEvaluatorWith(t, evaluateFlags)
	ctx := context.Background()
	adapter := typed.New(ev)

	for name, resolve := range map[string]func(){
		"typed methods": func() { _, _, _, _, _ = ev.ResolveBooleanValue(ctx, "id", "static-bool", nil) },
		"adapter":       func() { _, _, _, _, _ = adapter.ResolveBooleanValue(ctx, "id", "static-bool", nil) },
	} {
		allocs := testing.AllocsPerRun(200, resolve)
		require.LessOrEqual(t, allocs, float64(staticBoolAllocs), name)
		t.Logf("%s: %v allocs/op (baseline %d)", name, allocs, staticBoolAllocs)
	}
}

// Two flags that always change together resolve to the same round in one bulk request, however deliveries
// interleave with the request.
func TestBulkResolutionIsConsistent(t *testing.T) {
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"src"})
	require.NoError(t, err)
	ev := NewJSON(log, st)
	deliver := func(round int) {
		data := fmt.Sprintf(`{"flags": {
		  "a": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": %d}},
		  "b": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": %d}}}}`, round, round)
		require.NoError(t, ev.SetState(flagsync.DataSync{Source: "src", FlagData: data}))
	}
	deliver(0)

	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for round := 1; round <= 200; round++ {
			deliver(round)
		}
	}()
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				values, _, err := ev.ResolveAllValues(context.Background(), "id", nil)
				if err != nil || len(values) != 2 {
					t.Errorf("expected 2 values, observed %d and error %v", len(values), err)
					return
				}
				if values[0].Value != values[1].Value {
					t.Errorf("expected one round, observed %v and %v", values[0].Value, values[1].Value)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestEvaluateExplicitKeysAndMissingKey(t *testing.T) {
	ev, _ := newEvaluatorWith(t, evaluateFlags)

	results, _, err := ev.Evaluate(context.Background(), evaluator.Request{FlagKeys: []string{"static-bool", "missing"}}, nil)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.True(t, results[0].HasValue)
	require.Equal(t, "FLAG_NOT_FOUND", results[1].ErrorCode)

	bulk, _, err := ev.Evaluate(context.Background(), evaluator.Request{Bulk: true, FlagKeys: []string{"text"}}, nil)
	require.NoError(t, err)
	require.Len(t, bulk, 1)
	require.Equal(t, "text", bulk[0].FlagKey)
}

// flagdSamples describes flags for the contract.
func flagdSamples() evaluatortest.Samples {
	var mu sync.Mutex
	writers := map[*store.Store]*JSON{}
	writerFor := func(st *store.Store) *JSON {
		mu.Lock()
		defer mu.Unlock()
		if w, ok := writers[st]; ok {
			return w
		}
		w := NewJSON(logger.NewLogger(nil, false), st)
		writers[st] = w
		return w
	}
	deliver := func(st *store.Store, data string) error {
		return writerFor(st).SetState(flagsync.DataSync{Source: evaluatortest.Source, FlagData: data})
	}
	flags := func(round int) string {
		return fmt.Sprintf(`{"flags": {
		  "static-bool": {"state": "ENABLED", "defaultVariant": "on", "variants": {"on": true, "off": false}},
		  "static-int": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": 10}},
		  "static-string": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": "s"}},
		  "disabled": {"state": "DISABLED", "defaultVariant": "on", "variants": {"on": true, "off": false}},
		  "absent": {"state": "ENABLED", "variants": {"on": true, "off": false}},
		  "pair-a": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": %d}},
		  "pair-b": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": %d}}}}`, round, round)
	}
	return evaluatortest.Samples{
		Setup: func(st *store.Store) error { return deliver(st, flags(0)) },
		Static: []evaluatortest.StaticSample{
			{Key: "static-bool", Hint: evaluator.TypeBool, Value: true},
			{Key: "static-int", Hint: evaluator.TypeInt, Value: 10.0},
			{Key: "static-string", Hint: evaluator.TypeString, Value: "s"},
		},
		Disabled:      "disabled",
		AbsentDefault: "absent",
		Paired: &evaluatortest.PairedSample{
			Keys:  [2]string{"pair-a", "pair-b"},
			Write: func(st *store.Store, round int) error { return deliver(st, flags(round)) },
			Value: func(r evaluator.Result) (int, bool) {
				v, ok := r.Value.(float64)
				return int(v), ok
			},
		},
	}
}

func TestJsonLogicEvaluatorPassesTheContract(t *testing.T) {
	evaluatortest.Run(t, Factory{}, flagdSamples())
}

func TestFactory(t *testing.T) {
	f := Factory{}
	require.Equal(t, "flagd", f.ConfigurationFormat())
	require.NotEmpty(t, f.Type())

	_, err := f.Create(evaluator.Settings{})
	require.Error(t, err)

	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"src"})
	require.NoError(t, err)
	ev, err := f.Create(evaluator.Settings{Store: st, Logger: log, Config: map[string]any{"strictNumericRange": true}})
	require.NoError(t, err)
	require.True(t, ev.(*JSON).strictNumericRange)
}
