package flagsmith_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine"
	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine/engine_eval"
	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine/environments"
	"github.com/stretchr/testify/require"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/evaluatortest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"

	flagsmith "github.com/open-feature-forking/openfeature-relay/contrib/flagsmith/engine"
)

type feat struct {
	name    string
	enabled bool
	value   any
}

// envDoc writes the environment document of an environment with standard features.
func envDoc(apiKey string, hideDisabled bool, fs ...feat) string {
	states := make([]map[string]any, 0, len(fs))
	for i, f := range fs {
		states = append(states, map[string]any{
			"feature": map[string]any{"id": i + 1, "name": f.name, "type": "STANDARD"},
			"enabled": f.enabled, "feature_state_value": f.value, "django_id": i + 1,
		})
	}
	raw, err := json.Marshal(map[string]any{
		"id": 1, "name": apiKey, "api_key": apiKey, "updated_at": "2026-01-01T00:00:00Z",
		"project":            map[string]any{"hide_disabled_flags": hideDisabled, "segments": []any{}},
		"feature_states":     states,
		"identity_overrides": []any{},
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func deliver(t testing.TB, st *store.Store, source, data string) {
	t.Helper()
	format, err := flagsmith.Factory{}.Create(configformat.Settings{Store: st, Logger: logger.NewLogger(nil, false)})
	require.NoError(t, err)
	_, err = format.Accept(context.Background(), configformat.Input{Source: source, Data: data, IncrementalUpdate: true})
	require.NoError(t, err)
}

func contractSamples() evaluatortest.Samples {
	round := func(n int) string {
		return envDoc("ROUND", false, feat{"pair-a", true, n}, feat{"pair-b", true, n})
	}
	write := func(st *store.Store, n int) error {
		format, err := flagsmith.Factory{}.Create(configformat.Settings{Store: st})
		if err != nil {
			return err
		}
		_, err = format.Accept(context.Background(), configformat.Input{
			Source: evaluatortest.Source, Data: round(n), IncrementalUpdate: true,
		})
		return err
	}
	return evaluatortest.Samples{
		Setup: func(st *store.Store) error {
			format, err := flagsmith.Factory{}.Create(configformat.Settings{Store: st})
			if err != nil {
				return err
			}
			docs := []string{
				envDoc("STATIC", false,
					feat{"static-bool", true, true}, feat{"static-int", true, 10}, feat{"static-float", true, 1.5},
					feat{"static-string", true, "s"}, feat{"disabled", false, "x"}, feat{"no-value", true, nil}),
				envDoc("A", false, feat{"shared", true, "a"}),
				envDoc("B", false, feat{"shared", true, "b"}),
			}
			for _, d := range docs {
				if _, err := format.Accept(context.Background(), configformat.Input{
					Source: evaluatortest.Source, Data: d, IncrementalUpdate: true,
				}); err != nil {
					return err
				}
			}
			return write(st, 0)
		},
		Static: []evaluatortest.StaticSample{
			{Key: "static-bool", Hint: evaluator.TypeBool, Value: true},
			{Key: "static-int", Hint: evaluator.TypeInt, Value: 10.0},
			{Key: "static-float", Hint: evaluator.TypeFloat, Value: 1.5},
			{Key: "static-string", Hint: evaluator.TypeString, Value: "s"},
		},
		Disabled:      "disabled",
		AbsentDefault: "no-value",
		Shared:        &evaluatortest.SharedSample{Key: "shared", FlagSets: []string{"A", "B"}},
		Paired: &evaluatortest.PairedSample{
			Keys:  [2]string{"pair-a", "pair-b"},
			Write: write,
			Value: func(r evaluator.Result) (int, bool) {
				v, ok := r.Value.(float64)
				return int(v), ok
			},
		},
		// a request's attributes only decide whether a project hides its disabled flags: no flag resolves because of them
		Without: []evaluatortest.Feature{evaluatortest.FeatureAttribute},
	}
}

// TestEvaluatorContract covers the main scenario's steps 9 and 10 and A7: the engine module's evaluator meets every case
// of the evaluator contract with samples written in Flagsmith's environment documents.
func TestEvaluatorContract(t *testing.T) {
	evaluatortest.Run(t, flagsmith.EvaluatorFactory{}, contractSamples())
}

func TestFactory(t *testing.T) {
	var f evaluator.Factory = flagsmith.EvaluatorFactory{}
	require.NoError(t, evaluator.ValidateDeclarations(f))
	require.Equal(t, flagsmith.Type, f.ConfigurationFormat())

	_, err := f.Create(evaluator.Settings{})
	require.Error(t, err, "an evaluator needs a store")

	st, err := store.NewStore(logger.NewLogger(nil, false), []string{"src"})
	require.NoError(t, err)
	_, err = f.Create(evaluator.Settings{Store: st, Config: map[string]any{"booleanFromEnabled": "yes"}})
	require.ErrorContains(t, err, "booleanFromEnabled")
	_, err = f.Create(evaluator.Settings{Store: st, Config: map[string]any{"booleanFromEnabled": true}})
	require.NoError(t, err)
}

func newEvaluator(t testing.TB, config map[string]any, sources ...string) (evaluator.Evaluator, *store.Store) {
	t.Helper()
	st, err := store.NewStore(logger.NewLogger(nil, false), sources)
	require.NoError(t, err)
	ev, err := flagsmith.EvaluatorFactory{}.Create(evaluator.Settings{Store: st, Config: config})
	require.NoError(t, err)
	return ev, st
}

func run(t testing.TB, ev evaluator.Evaluator, req evaluator.Request) []evaluator.Result {
	t.Helper()
	results, _, err := ev.Evaluate(context.Background(), req, nil)
	require.NoError(t, err)
	return results
}

func one(t testing.TB, ev evaluator.Evaluator, key string, hint evaluator.Type, ctx map[string]any, attributes map[string]any) evaluator.Result {
	t.Helper()
	results := run(t, ev, evaluator.Request{FlagKey: key, TypeHint: hint, Context: ctx, Attributes: attributes})
	require.Len(t, results, 1)
	return results[0]
}

func bulk(t testing.TB, ev evaluator.Evaluator, ctx map[string]any, attributes map[string]any) map[string]evaluator.Result {
	t.Helper()
	out := map[string]evaluator.Result{}
	for _, r := range run(t, ev, evaluator.Request{Bulk: true, Context: ctx, Attributes: attributes}) {
		out[r.FlagKey] = r
	}
	return out
}

func basicEvaluator(t testing.TB, config map[string]any) evaluator.Evaluator {
	t.Helper()
	ev, st := newEvaluator(t, config, "src")
	deliver(t, st, "src", document(t, "basic.json"))
	return ev
}

func user(id string, traits ...any) map[string]any {
	ctx := map[string]any{"targetingKey": id}
	for i := 0; i+1 < len(traits); i += 2 {
		ctx[traits[i].(string)] = traits[i+1]
	}
	return ctx
}

func TestValuesByType(t *testing.T) {
	ev := basicEvaluator(t, nil)

	for _, tc := range []struct {
		key  string
		hint evaluator.Type
		want any
	}{
		{"welcome", evaluator.TypeString, "hello"},
		{"welcome", evaluator.TypeAny, "hello"},
		{"limit", evaluator.TypeInt, 10.0},
		{"limit", evaluator.TypeFloat, 10.0},
		{"ratio", evaluator.TypeFloat, 0.5},
		{"config", evaluator.TypeObject, map[string]any{"a": 1.0}},
	} {
		r := one(t, ev, tc.key, tc.hint, nil, nil)
		require.True(t, r.HasValue, tc.key)
		require.Equal(t, tc.want, r.Value, tc.key)
		require.Equal(t, model.StaticReason, r.Reason, tc.key)
		require.Empty(t, r.ErrorCode, tc.key)
		require.Equal(t, "ENVKEY1", r.FlagSet)
		require.Equal(t, "ENVKEY1", r.Metadata["flagSetId"])
	}
}

func TestTypeMismatch(t *testing.T) {
	ev := basicEvaluator(t, nil)
	for _, tc := range []struct {
		key  string
		hint evaluator.Type
	}{
		{"welcome", evaluator.TypeBool},
		{"welcome", evaluator.TypeInt},
		{"limit", evaluator.TypeString},
		{"limit", evaluator.TypeObject},
		{"ratio", evaluator.TypeInt},
		{"welcome", evaluator.TypeObject},
	} {
		r := one(t, ev, tc.key, tc.hint, nil, nil)
		require.Equal(t, model.TypeMismatchErrorCode, r.ErrorCode, "%s as %d", tc.key, tc.hint)
		require.Equal(t, model.ErrorReason, r.Reason)
		require.False(t, r.HasValue)
	}
}

func TestUnknownKey(t *testing.T) {
	ev := basicEvaluator(t, nil)
	r := one(t, ev, "no-such-flag", evaluator.TypeAny, nil, nil)
	require.Equal(t, model.FlagNotFoundErrorCode, r.ErrorCode)
	require.False(t, r.HasValue)
}

// TestReasons pins UC-019 Q4: the engine's reasons as OpenFeature's, with the segment and the weight in the metadata.
func TestReasons(t *testing.T) {
	ev := basicEvaluator(t, nil)

	env := one(t, ev, "banner", evaluator.TypeString, nil, nil)
	require.Equal(t, "default-banner", env.Value)
	require.Equal(t, model.StaticReason, env.Reason)
	require.NotContains(t, env.Metadata, "flagsmithSegment")
	require.Equal(t, 6, env.Metadata["flagsmithFeatureId"])

	// traits without a targeting key belong to no identity: the evaluation stays at environment level
	require.Equal(t, "default-banner", one(t, ev, "banner", evaluator.TypeString, map[string]any{"plan": "beta"}, nil).Value)

	beta := one(t, ev, "banner", evaluator.TypeString, user("u1", "plan", "beta"), nil)
	require.Equal(t, "beta-banner", beta.Value)
	require.Equal(t, model.TargetingMatchReason, beta.Reason)
	require.Equal(t, "beta users", beta.Metadata["flagsmithSegment"])

	free := one(t, ev, "banner", evaluator.TypeString, user("u1", "plan", "free"), nil)
	require.Equal(t, "default-banner", free.Value)
	require.Equal(t, model.StaticReason, free.Reason)

	seen := map[string]int{}
	for i := range 300 {
		r := one(t, ev, "experiment", evaluator.TypeString, user(fmt.Sprintf("u%d", i)), nil)
		seen[r.Variant]++
		switch r.Variant {
		case "variant-a", "variant-b":
			require.Equal(t, model.SplitReason, r.Reason)
			require.Equal(t, 30.0, r.Metadata["flagsmithWeight"])
			require.Equal(t, "value-"+strings.TrimPrefix(r.Variant, "variant-"), r.Value)
		case "control":
			require.Equal(t, model.StaticReason, r.Reason)
			require.Equal(t, "control-value", r.Value)
		default:
			t.Fatalf("unexpected variant %q", r.Variant)
		}
	}
	require.Len(t, seen, 3, "30%% + 30%% + a control bucket of 40%% over 300 identities: %v", seen)

	// no identity, no split
	noID := one(t, ev, "experiment", evaluator.TypeString, nil, nil)
	require.Empty(t, noID.Variant)
	require.Equal(t, "control-value", noID.Value)
	require.Equal(t, model.StaticReason, noID.Reason)
}

// TestBooleanValueMode pins UC-019 Q5's default: Flagsmith's OpenFeature providers read a boolean from the value of a
// flag. A flag without a value, and a disabled flag, resolve to none, so the caller's default stands.
func TestBooleanValueMode(t *testing.T) {
	ev := basicEvaluator(t, nil)

	noValue := one(t, ev, "dark-mode", evaluator.TypeBool, nil, nil)
	require.False(t, noValue.HasValue)
	require.Empty(t, noValue.ErrorCode)
	require.Equal(t, model.DefaultReason, noValue.Reason)

	disabled := one(t, ev, "kill-switch", evaluator.TypeBool, nil, nil)
	require.False(t, disabled.HasValue)
	require.Empty(t, disabled.ErrorCode)
	require.Equal(t, model.DisabledReason, disabled.Reason)

	// an identity override that disables a flag disables it for that identity only
	require.Equal(t, model.DisabledReason, one(t, ev, "dark-mode", evaluator.TypeBool, user("vip-1"), nil).Reason)
	require.Equal(t, model.DefaultReason, one(t, ev, "dark-mode", evaluator.TypeBool, user("someone-else"), nil).Reason)
}

// TestBooleanEnabledMode pins UC-019 Q5's setting: a boolean request resolves to the enabled state. A disabled flag is
// false with the reason DISABLED, not none. Other types read the value as before.
func TestBooleanEnabledMode(t *testing.T) {
	ev := basicEvaluator(t, map[string]any{"booleanFromEnabled": true})

	on := one(t, ev, "dark-mode", evaluator.TypeBool, nil, nil)
	require.True(t, on.HasValue)
	require.Equal(t, true, on.Value)
	require.Equal(t, model.StaticReason, on.Reason)

	off := one(t, ev, "kill-switch", evaluator.TypeBool, nil, nil)
	require.True(t, off.HasValue)
	require.Equal(t, false, off.Value)
	require.Equal(t, model.DisabledReason, off.Reason)

	override := one(t, ev, "dark-mode", evaluator.TypeBool, user("vip-1"), nil)
	require.Equal(t, false, override.Value)
	require.Equal(t, model.DisabledReason, override.Reason)

	// other types are not affected, a disabled flag has no value
	require.Equal(t, "hello", one(t, ev, "welcome", evaluator.TypeString, nil, nil).Value)
	require.False(t, one(t, ev, "kill-switch", evaluator.TypeString, nil, nil).HasValue)

	// a bulk request has no type: a flag whose value is none or a boolean is read as a boolean
	all := bulk(t, ev, nil, nil)
	require.Equal(t, true, all["dark-mode"].Value)
	require.Equal(t, "hello", all["welcome"].Value)
	require.False(t, all["kill-switch"].HasValue, "its value is text")
	require.Equal(t, model.DisabledReason, all["kill-switch"].Reason)

	// BR-007: without a targeting key the evaluation is at environment level, so the identity override does not apply
	require.Equal(t, true, bulk(t, ev, nil, nil)["dark-mode"].Value)
	require.Equal(t, false, bulk(t, ev, user("vip-1"), nil)["dark-mode"].Value)
}

func TestIdentityOverrideValueIsText(t *testing.T) {
	ev, st := newEvaluator(t, nil, "src")
	base := envDoc("K", false, feat{"flag", true, true}, feat{"n", true, 1})
	doc := strings.Replace(base, `"identity_overrides":[]`, `"identity_overrides":[{"identifier":"vip","identity_features":[
		{"feature":{"id":1,"name":"flag"},"enabled":true,"feature_state_value":false},
		{"feature":{"id":2,"name":"n"},"enabled":true,"feature_state_value":7}]}]`, 1)
	deliver(t, st, "src", doc)

	require.Equal(t, true, one(t, ev, "flag", evaluator.TypeBool, user("other"), nil).Value)
	r := one(t, ev, "flag", evaluator.TypeBool, user("vip"), nil)
	require.Equal(t, false, r.Value, "the override's value reads as the boolean it was written as")
	require.Equal(t, model.TargetingMatchReason, r.Reason)
	require.Equal(t, "identity_overrides", one(t, ev, "n", evaluator.TypeInt, user("vip"), nil).Metadata["flagsmithSegment"])
	require.Equal(t, 7.0, one(t, ev, "n", evaluator.TypeInt, user("vip"), nil).Value)

	// an override that states no value writes empty text: still no value for a boolean request, and the empty string for a string one
	deliver(t, st, "src", strings.Replace(base, `"identity_overrides":[]`, `"identity_overrides":[{"identifier":"vip","identity_features":[
		{"feature":{"id":1,"name":"flag"},"enabled":true,"feature_state_value":null}]}]`, 1))
	noValue := one(t, ev, "flag", evaluator.TypeBool, user("vip"), nil)
	require.False(t, noValue.HasValue)
	require.Empty(t, noValue.ErrorCode)
	require.Equal(t, "", one(t, ev, "flag", evaluator.TypeString, user("vip"), nil).Value)
}

func TestHideDisabledForClientCredential(t *testing.T) {
	ev, st := newEvaluator(t, nil, "src")
	deliver(t, st, "src", replaceOnce(t, document(t, "basic.json"), `"hide_disabled_flags": false`, `"hide_disabled_flags": true`))
	client := map[string]any{flagsmith.CredentialAttribute: flagsmith.CredentialClient}
	server := map[string]any{flagsmith.CredentialAttribute: "server"}

	r := one(t, ev, "kill-switch", evaluator.TypeAny, nil, client)
	require.Equal(t, model.FlagNotFoundErrorCode, r.ErrorCode)
	require.NotContains(t, bulk(t, ev, nil, client), "kill-switch")
	require.Len(t, bulk(t, ev, nil, client), len(basicKeys)-1)

	for _, attributes := range []map[string]any{server, nil} {
		r = one(t, ev, "kill-switch", evaluator.TypeAny, nil, attributes)
		require.Equal(t, model.DisabledReason, r.Reason)
		require.Empty(t, r.ErrorCode)
		require.Len(t, bulk(t, ev, nil, attributes), len(basicKeys))
	}

	// a flag disabled for one identity only is hidden from the identities it is disabled for
	require.Equal(t, model.FlagNotFoundErrorCode, one(t, ev, "dark-mode", evaluator.TypeAny, user("vip-1"), client).ErrorCode)
	require.Empty(t, one(t, ev, "dark-mode", evaluator.TypeAny, user("other"), client).ErrorCode)
}

func TestDisabledFlagsStayWhenProjectDoesNotHideThem(t *testing.T) {
	ev := basicEvaluator(t, nil)
	client := map[string]any{flagsmith.CredentialAttribute: flagsmith.CredentialClient}
	require.Len(t, bulk(t, ev, nil, client), len(basicKeys))
	require.Equal(t, model.DisabledReason, one(t, ev, "kill-switch", evaluator.TypeAny, nil, client).Reason)
}

// TestFlagSetsAreEnvironments covers BR-002: environments hold the same flag keys, and a key resolves only within the
// flag set the selector names.
func TestFlagSetsAreEnvironments(t *testing.T) {
	ev, st := newEvaluator(t, nil, "one", "two")
	deliver(t, st, "one", envDoc("ENV1", false, feat{"welcome", true, "from one"}, feat{"only-one", true, 1}))
	deliver(t, st, "two", envDoc("ENV2", false, feat{"welcome", true, "from two"}))

	for flagSet, want := range map[string]string{"ENV1": "from one", "ENV2": "from two"} {
		selector, err := store.NewSelector("flagSetId=" + flagSet)
		require.NoError(t, err)
		results := run(t, ev, evaluator.Request{Selector: selector, FlagKey: "welcome", TypeHint: evaluator.TypeString})
		require.Len(t, results, 1)
		require.Equal(t, want, results[0].Value)
		require.Equal(t, flagSet, results[0].FlagSet)

		all := run(t, ev, evaluator.Request{Selector: selector, Bulk: true})
		for _, r := range all {
			require.Equal(t, flagSet, r.FlagSet)
		}
	}

	selector, err := store.NewSelector("flagSetId=ENV2")
	require.NoError(t, err)
	r := run(t, ev, evaluator.Request{Selector: selector, FlagKey: "only-one"})[0]
	require.Equal(t, model.FlagNotFoundErrorCode, r.ErrorCode, "a key of another environment is not found")

	// without a selector both environments answer, once per flag set
	var sets []string
	for _, r := range run(t, ev, evaluator.Request{Bulk: true}) {
		if r.FlagKey == "welcome" {
			sets = append(sets, r.FlagSet)
		}
	}
	require.Equal(t, []string{"ENV1", "ENV2"}, sets)
}

func TestLaterSourceWinsAFlagSetKey(t *testing.T) {
	ev, st := newEvaluator(t, nil, "first", "second")
	deliver(t, st, "first", envDoc("ENV", false, feat{"welcome", true, "first"}))
	deliver(t, st, "second", envDoc("ENV", false, feat{"welcome", true, "second"}))

	require.Equal(t, "second", one(t, ev, "welcome", evaluator.TypeString, nil, nil).Value)
	selector, err := store.NewSelector("source=first")
	require.NoError(t, err)
	require.Equal(t, "first", run(t, ev, evaluator.Request{Selector: selector, FlagKey: "welcome"})[0].Value)
}

func TestExplicitKeysOfABulkRequest(t *testing.T) {
	ev := basicEvaluator(t, nil)
	results := run(t, ev, evaluator.Request{Bulk: true, FlagKeys: []string{"welcome", "limit", "missing"}})
	require.Len(t, results, 3)
	require.Equal(t, "welcome", results[0].FlagKey)
	require.Equal(t, "hello", results[0].Value)
	require.Equal(t, 10.0, results[1].Value)
	require.Equal(t, model.FlagNotFoundErrorCode, results[2].ErrorCode)
}

// engineExpectation computes what Flagsmith's engine says about an identity, from a document decoded without this
// module's format and with the identity written out by hand, so that the key of the identity is pinned too.
func engineExpectation(t testing.TB, ec engine_eval.EngineEvaluationContext, identifier string, traits map[string]any) map[string]*engine_eval.FlagResult {
	t.Helper()
	if identifier != "" {
		ec.Identity = &engine_eval.IdentityContext{
			Identifier: identifier, Key: ec.Environment.Key + "_" + identifier, Traits: traits,
		}
	}
	return flagengine.GetEvaluationResult(&ec).Flags
}

func expectedReason(engineReason string) string {
	switch {
	case engineReason == "DEFAULT":
		return model.StaticReason
	case strings.HasPrefix(engineReason, "TARGETING_MATCH"):
		return model.TargetingMatchReason
	case strings.HasPrefix(engineReason, "SPLIT"):
		return model.SplitReason
	}
	return model.UnknownReason
}

// TestEvaluateMatchesFlagengine pins the mapping layer: for a corpus of identities and traits, every flag the module
// returns equals what Flagsmith's engine returns for the same environment document.
func TestEvaluateMatchesFlagengine(t *testing.T) {
	doc := document(t, "basic.json")
	var env environments.EnvironmentModel
	// the Go client's model needs a zone on updated_at, which the format does not
	require.NoError(t, json.Unmarshal([]byte(replaceOnce(t, doc, `"2026-01-02T03:04:05"`, `"2026-01-02T03:04:05Z"`)), &env))
	ec := engine_eval.MapEnvironmentDocumentToEvaluationContext(&env)

	ev := basicEvaluator(t, nil)

	identifiers := []string{""}
	for i := range 300 {
		identifiers = append(identifiers, fmt.Sprintf("user-%d", i))
	}
	identifiers = append(identifiers, "vip-1")
	variants := map[string]bool{}
	for i, identifier := range identifiers {
		var traits map[string]any
		ctx := map[string]any{}
		if identifier != "" {
			ctx["targetingKey"] = identifier
			traits = map[string]any{"plan": []string{"beta", "free", "gamma"}[i%3], "age": float64(18 + i%40)}
			for k, v := range traits {
				ctx[k] = v
			}
		}
		want := engineExpectation(t, ec, identifier, traits)
		got := bulk(t, ev, ctx, nil)
		require.Len(t, got, len(want), identifier)

		for name, fr := range want {
			r, ok := got[name]
			require.True(t, ok, "%s for %q", name, identifier)
			where := fmt.Sprintf("%s for %q", name, identifier)
			switch {
			case !fr.Enabled:
				require.Equal(t, model.DisabledReason, r.Reason, where)
				require.False(t, r.HasValue, where)
			case fr.Value == nil:
				require.Equal(t, model.DefaultReason, r.Reason, where)
				require.False(t, r.HasValue, where)
			default:
				require.True(t, r.HasValue, where)
				require.Equal(t, fr.Value, r.Value, where)
				require.Equal(t, fr.Variant, r.Variant, where)
				require.Equal(t, expectedReason(fr.Reason), r.Reason, where)
				variants[r.Variant] = true
			}
		}

		// a single request agrees with the bulk request
		single := one(t, ev, "experiment", evaluator.TypeAny, ctx, nil)
		require.Equal(t, got["experiment"].Value, single.Value, identifier)
		require.Equal(t, got["experiment"].Variant, single.Variant, identifier)
	}
	require.Contains(t, variants, "variant-a")
	require.Contains(t, variants, "variant-b")
	require.Contains(t, variants, "control")
}
