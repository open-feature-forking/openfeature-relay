package evaluation

import (
	"context"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const kvFormat = "kv"

// kvEvaluator resolves every key the kv documents of the store hold to "kv".
type kvEvaluator struct{ st *store.Store }

func (e kvEvaluator) Evaluate(_ context.Context, req evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	keys := req.FlagKeys
	if keys == nil {
		keys = []string{req.FlagKey}
	}
	for _, key := range keys {
		dst = append(dst, evaluator.Result{
			FlagKey: key, Value: "kv", HasValue: true, Variant: "v", Reason: model.StaticReason,
			Metadata: model.Metadata{"flagSetId": ""},
		})
	}
	return dst, nil, nil
}

type kvFactory struct{ format string }

func (kvFactory) Type() string                  { return "kv-evaluator" }
func (f kvFactory) ConfigurationFormat() string { return f.format }
func (kvFactory) Create(s evaluator.Settings) (evaluator.Evaluator, error) {
	return kvEvaluator{st: s.Store}, nil
}

func deliverKV(t *testing.T, st *store.Store, source string, keys ...string) {
	t.Helper()
	require.NoError(t, st.Apply(store.ApplyRequest{
		Format: kvFormat, Source: source,
		Documents: []store.Document{{Format: kvFormat, Source: source, FlagKeys: keys}},
	}))
}

const mixedFlagd = `{"flags": {
  "text": {"state": "ENABLED", "defaultVariant": "text", "variants": {"text": "hello", "number": 42},
    "targeting": {"if": [{"==": [{"var": "email"}, "a@b.c"]}, "number", "text"]}},
  "flagd-only": {"state": "ENABLED", "defaultVariant": "on", "variants": {"on": true, "off": false}}
}}`

func mixedDistribution(t *testing.T) evaluator.IEvaluator {
	t.Helper()
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"flagd.json", "kv.json"})
	require.NoError(t, err)
	ev, err := NewRoutedEvaluator(log, st, []string{kvFormat}, []evaluator.Factory{kvFactory{kvFormat}})
	require.NoError(t, err)
	require.NoError(t, ev.SetState(sync.DataSync{Source: "flagd.json", FlagData: mixedFlagd}))
	deliverKV(t, st, "kv.json", "kv-only", "text")
	return ev
}

// A flagd variant that does not have the type of the flag's default variant is a mismatch in a mixed
// distribution as in flagd-full, and flagd keeps its flags.
func TestRouterMixedBulkKeepsFlagdTypeRule(t *testing.T) {
	ev := mixedDistribution(t)

	values, _, err := ev.ResolveAllValues(context.Background(), "id", map[string]any{"email": "a@b.c"})
	require.NoError(t, err)
	byKey := map[string]evaluator.AnyValue{}
	for _, v := range values {
		byKey[v.FlagKey] = v
	}
	require.Len(t, byKey, 3)
	require.Equal(t, true, byKey["flagd-only"].Value)
	require.Equal(t, "kv", byKey["kv-only"].Value)
	// "text" is in both sources and the later one wins
	require.Equal(t, "kv", byKey["text"].Value)

	// without the kv source taking the key, the flagd rule applies
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"flagd.json", "kv.json"})
	require.NoError(t, err)
	ev, err = NewRoutedEvaluator(log, st, []string{kvFormat}, []evaluator.Factory{kvFactory{kvFormat}})
	require.NoError(t, err)
	require.NoError(t, ev.SetState(sync.DataSync{Source: "flagd.json", FlagData: mixedFlagd}))
	deliverKV(t, st, "kv.json", "kv-only")

	values, _, err = ev.ResolveAllValues(context.Background(), "id", map[string]any{"email": "a@b.c"})
	require.NoError(t, err)
	byKey = map[string]evaluator.AnyValue{}
	for _, v := range values {
		byKey[v.FlagKey] = v
	}
	require.Len(t, byKey, 3)
	require.EqualError(t, byKey["text"].Error, model.TypeMismatchErrorCode)
	require.Equal(t, "kv", byKey["kv-only"].Value)
}

func TestRoutedSingleResolutionsReachTheOwningEvaluator(t *testing.T) {
	ev := mixedDistribution(t)
	ctx := context.Background()

	on, _, _, _, err := ev.ResolveBooleanValue(ctx, "id", "flagd-only", nil)
	require.NoError(t, err)
	require.True(t, on)

	got, _, _, _, err := ev.ResolveStringValue(ctx, "id", "kv-only", nil)
	require.NoError(t, err)
	require.Equal(t, "kv", got)

	_, _, _, _, err = ev.ResolveStringValue(ctx, "id", "missing", nil)
	require.EqualError(t, err, model.FlagNotFoundErrorCode)
}

func TestEvaluatorWithoutFormatFailsStartup(t *testing.T) {
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"src"})
	require.NoError(t, err)

	_, err = NewRoutedEvaluator(log, st, []string{"other"}, []evaluator.Factory{kvFactory{kvFormat}})
	require.Error(t, err)
	require.Contains(t, err.Error(), `"kv"`)
	require.Contains(t, err.Error(), "flagd, other")
}

func TestFormatWithoutEvaluatorResolvesNotFound(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	log := logger.NewLogger(zap.New(core), false)
	st, err := store.NewStore(log, []string{"flagd.json", "kv.json"})
	require.NoError(t, err)

	ev, err := NewRoutedEvaluator(log, st, []string{kvFormat}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, logs.FilterMessageSnippet(`"kv" has no evaluator`).Len())

	require.NoError(t, ev.SetState(sync.DataSync{Source: "flagd.json", FlagData: mixedFlagd}))
	deliverKV(t, st, "kv.json", "kv-only")

	_, _, _, _, err = ev.ResolveStringValue(context.Background(), "id", "kv-only", nil)
	require.EqualError(t, err, model.FlagNotFoundErrorCode)

	values, _, err := ev.ResolveAllValues(context.Background(), "id", nil)
	require.NoError(t, err)
	require.Len(t, values, 2, "only flagd's flags")
}

// Behind the router, flagd-full resolves a flag with the allocations of the evaluator on its own (NFR-001).
func TestRoutedSingleEvaluatorAllocs(t *testing.T) {
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"src"})
	require.NoError(t, err)
	routed := NewEvaluator(log, st)
	direct := flagdeval.NewJSON(log, st)
	require.NoError(t, routed.SetState(sync.DataSync{Source: "src", FlagData: mixedFlagd}))

	ctx := context.Background()
	viaRouter := testing.AllocsPerRun(200, func() { _, _, _, _, _ = routed.ResolveBooleanValue(ctx, "id", "flagd-only", nil) })
	alone := testing.AllocsPerRun(200, func() { _, _, _, _, _ = direct.ResolveBooleanValue(ctx, "id", "flagd-only", nil) })
	// under -race a sync.Pool drops some of the buffers it is given, which adds an allocation to a run now and then
	require.LessOrEqual(t, viaRouter, alone+1)
}

func TestNewEvaluatorOverAnotherStoreServesWithoutRouter(t *testing.T) {
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"src"})
	require.NoError(t, err)
	ev := NewEvaluator(log, foreignStore{st})
	_, ok := ev.(*flagdeval.JSON)
	require.True(t, ok)
}

type foreignStore struct{ store.IStore }
