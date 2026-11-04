package flagsmith_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
)

func condition(operator, property, value string) string {
	return fmt.Sprintf(`{"operator":%q,"property_":%q,"value":%q}`, operator, property, value)
}

func rule(kind string, conditions []string, rules ...string) string {
	return fmt.Sprintf(`{"type":%q,"conditions":[%s],"rules":[%s]}`, kind, strings.Join(conditions, ","), strings.Join(rules, ","))
}

// segmentJSON is a segment that overrides the feature "f" with the value "on" at the priority, for the rules.
func segmentJSON(id int, name string, priority int, value string, rules ...string) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"rules":[%s],"feature_states":[{"feature":{"id":1,"name":"f"},"enabled":true,
		"feature_state_value":%q,"feature_segment":{"priority":%d},"django_id":%d}]}`,
		id, name, strings.Join(rules, ","), value, priority, 100+id)
}

func segmentsDoc(identityOverrides string, segments ...string) string {
	if identityOverrides == "" {
		identityOverrides = "[]"
	}
	return fmt.Sprintf(`{"api_key":"ENV","project":{"segments":[%s]},
		"feature_states":[{"feature":{"id":1,"name":"f"},"enabled":true,"feature_state_value":"off","django_id":1}],
		"identity_overrides":%s}`, strings.Join(segments, ","), identityOverrides)
}

type traits = map[string]any

// TestSegmentOperators states, for each operator of a segment condition, the traits that match it and the ones that do
// not. The expectations are Flagsmith's documented behaviour, not read back from its engine.
func TestSegmentOperators(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rules string
		match []traits
		no    []traits
	}{
		{"EQUAL", rule("ALL", []string{condition("EQUAL", "plan", "beta")}),
			[]traits{{"plan": "beta"}}, []traits{{"plan": "free"}, {}}},
		{"EQUAL numbers", rule("ALL", []string{condition("EQUAL", "age", "30")}),
			[]traits{{"age": 30.0}, {"age": "30"}}, []traits{{"age": 31.0}}},
		{"EQUAL booleans", rule("ALL", []string{condition("EQUAL", "admin", "true")}),
			[]traits{{"admin": true}}, []traits{{"admin": false}}},
		{"NOT_EQUAL", rule("ALL", []string{condition("NOT_EQUAL", "plan", "beta")}),
			[]traits{{"plan": "free"}}, []traits{{"plan": "beta"}, {}}},
		{"GREATER_THAN", rule("ALL", []string{condition("GREATER_THAN", "age", "30")}),
			[]traits{{"age": 31.0}, {"age": "31"}}, []traits{{"age": 30.0}, {"age": 29.0}, {}}},
		{"GREATER_THAN fractions", rule("ALL", []string{condition("GREATER_THAN", "score", "1.5")}),
			[]traits{{"score": 1.6}}, []traits{{"score": 1.5}, {"score": 1.4}}},
		{"GREATER_THAN_INCLUSIVE", rule("ALL", []string{condition("GREATER_THAN_INCLUSIVE", "age", "30")}),
			[]traits{{"age": 30.0}, {"age": 31.0}}, []traits{{"age": 29.0}}},
		{"LESS_THAN", rule("ALL", []string{condition("LESS_THAN", "age", "30")}),
			[]traits{{"age": 29.0}}, []traits{{"age": 30.0}, {"age": 31.0}}},
		{"LESS_THAN_INCLUSIVE", rule("ALL", []string{condition("LESS_THAN_INCLUSIVE", "age", "30")}),
			[]traits{{"age": 30.0}, {"age": 29.0}}, []traits{{"age": 31.0}}},
		{"CONTAINS", rule("ALL", []string{condition("CONTAINS", "email", "@example.com")}),
			[]traits{{"email": "a@example.com"}}, []traits{{"email": "a@example.org"}, {}}},
		{"NOT_CONTAINS", rule("ALL", []string{condition("NOT_CONTAINS", "email", "@example.com")}),
			[]traits{{"email": "a@example.org"}}, []traits{{"email": "a@example.com"}}},
		{"REGEX", rule("ALL", []string{condition("REGEX", "code", "^ab+$")}),
			[]traits{{"code": "abbb"}}, []traits{{"code": "ba"}, {"code": "xabbb"}}},
		{"IN", rule("ALL", []string{condition("IN", "country", "at,de,ch")}),
			[]traits{{"country": "de"}}, []traits{{"country": "fr"}, {}}},
		{"IS_SET", rule("ALL", []string{condition("IS_SET", "plan", "")}),
			[]traits{{"plan": "x"}}, []traits{{}}},
		{"IS_NOT_SET", rule("ALL", []string{condition("IS_NOT_SET", "plan", "")}),
			[]traits{{}}, []traits{{"plan": "x"}}},
		{"MODULO", rule("ALL", []string{condition("MODULO", "n", "3|1")}),
			[]traits{{"n": 7.0}, {"n": 1.0}}, []traits{{"n": 8.0}, {"n": "text"}}},
		{"semver EQUAL", rule("ALL", []string{condition("EQUAL", "version", "1.2.3:semver")}),
			[]traits{{"version": "1.2.3"}}, []traits{{"version": "1.2.4"}}},
		{"semver GREATER_THAN", rule("ALL", []string{condition("GREATER_THAN", "version", "1.2.3:semver")}),
			[]traits{{"version": "1.2.4"}, {"version": "1.10.0"}}, []traits{{"version": "1.2.3"}, {"version": "1.2.2"}}},
		{"semver LESS_THAN", rule("ALL", []string{condition("LESS_THAN", "version", "1.2.3:semver")}),
			[]traits{{"version": "1.2.2"}}, []traits{{"version": "1.2.3"}, {"version": "not a version"}}},
		{"ALL needs every condition",
			rule("ALL", []string{condition("EQUAL", "plan", "beta"), condition("GREATER_THAN", "age", "17")}),
			[]traits{{"plan": "beta", "age": 18.0}}, []traits{{"plan": "beta", "age": 17.0}, {"plan": "free", "age": 18.0}}},
		{"ANY needs one condition",
			rule("ANY", []string{condition("EQUAL", "plan", "beta"), condition("EQUAL", "plan", "gamma")}),
			[]traits{{"plan": "beta"}, {"plan": "gamma"}}, []traits{{"plan": "free"}}},
		{"NONE needs no condition",
			rule("NONE", []string{condition("EQUAL", "plan", "beta"), condition("EQUAL", "plan", "gamma")}),
			[]traits{{"plan": "free"}}, []traits{{"plan": "beta"}, {"plan": "gamma"}}},
		{"nested rules",
			rule("ALL", nil,
				rule("ANY", []string{condition("EQUAL", "plan", "beta"), condition("EQUAL", "plan", "gamma")}),
				rule("NONE", []string{condition("EQUAL", "banned", "true")})),
			[]traits{{"plan": "beta"}, {"plan": "gamma", "banned": false}},
			[]traits{{"plan": "free"}, {"plan": "beta", "banned": true}}},
		{"identity as property", rule("ALL", []string{condition("EQUAL", "$.identity.identifier", "i")}),
			[]traits{{}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, st := newEvaluator(t, nil, "src")
			deliver(t, st, "src", segmentsDoc("", segmentJSON(1, "segment", 0, "on", tc.rules)))

			for _, tr := range tc.match {
				r := one(t, ev, "f", evaluator.TypeString, withTraits("i", tr), nil)
				require.Equal(t, "on", r.Value, "%v should match", tr)
				require.Equal(t, model.TargetingMatchReason, r.Reason, "%v", tr)
				require.Equal(t, "segment", r.Metadata["flagsmithSegment"])
			}
			for _, tr := range tc.no {
				r := one(t, ev, "f", evaluator.TypeString, withTraits("i", tr), nil)
				require.Equal(t, "off", r.Value, "%v should not match", tr)
				require.Equal(t, model.StaticReason, r.Reason, "%v", tr)
			}
		})
	}
}

func withTraits(id string, tr traits) map[string]any {
	ctx := map[string]any{"targetingKey": id}
	for k, v := range tr {
		ctx[k] = v
	}
	return ctx
}

func TestIdentityAsSegmentProperty(t *testing.T) {
	ev, st := newEvaluator(t, nil, "src")
	deliver(t, st, "src", segmentsDoc("", segmentJSON(1, "s", 0, "on", rule("ALL", []string{condition("EQUAL", "$.identity.identifier", "vip")}))))
	require.Equal(t, "on", one(t, ev, "f", evaluator.TypeString, user("vip"), nil).Value)
	require.Equal(t, "off", one(t, ev, "f", evaluator.TypeString, user("other"), nil).Value)
}

// TestSegmentPriority: of the segments that match, the one with the lowest priority number overrides the feature.
func TestSegmentPriority(t *testing.T) {
	ev, st := newEvaluator(t, nil, "src")
	any := rule("ALL", []string{condition("IS_SET", "plan", "")})
	deliver(t, st, "src", segmentsDoc("",
		segmentJSON(1, "late", 5, "from-late", any),
		segmentJSON(2, "first", 1, "from-first", any),
		segmentJSON(3, "other", 3, "from-other", rule("ALL", []string{condition("EQUAL", "plan", "gamma")}))))

	r := one(t, ev, "f", evaluator.TypeString, user("i", "plan", "beta"), nil)
	require.Equal(t, "from-first", r.Value)
	require.Equal(t, "first", r.Metadata["flagsmithSegment"])
}

// TestIdentityOverrides: an identity override beats every segment, applies to the listed identities only, and groups of
// identities with the same overrides share a segment of the engine.
func TestIdentityOverrides(t *testing.T) {
	overrides := `[
		{"identifier":"v1","identity_features":[{"feature":{"id":1,"name":"f"},"enabled":true,"feature_state_value":"vip"}]},
		{"identifier":"v2","identity_features":[{"feature":{"id":1,"name":"f"},"enabled":true,"feature_state_value":"vip"}]},
		{"identifier":"v3","identity_features":[{"feature":{"id":1,"name":"f"},"enabled":true,"feature_state_value":"other"}]},
		{"identifier":"v4","identity_features":[{"feature":{"id":1,"name":"f"},"enabled":false,"feature_state_value":"off-for-v4"}]},
		{"identifier":"v5","identity_features":[]}]`
	ev, st := newEvaluator(t, nil, "src")
	deliver(t, st, "src", segmentsDoc(overrides, segmentJSON(1, "everyone", 0, "from-segment", rule("ALL", []string{condition("IS_SET", "plan", "")}))))

	for id, want := range map[string]string{"v1": "vip", "v2": "vip", "v3": "other"} {
		r := one(t, ev, "f", evaluator.TypeString, user(id, "plan", "beta"), nil)
		require.Equal(t, want, r.Value, id)
		require.Equal(t, model.TargetingMatchReason, r.Reason, id)
	}
	require.Equal(t, "from-segment", one(t, ev, "f", evaluator.TypeString, user("v5", "plan", "beta"), nil).Value)
	require.Equal(t, "from-segment", one(t, ev, "f", evaluator.TypeString, user("nobody", "plan", "beta"), nil).Value)
	// the override needs no trait, and an override that disables the feature disables it for that identity
	require.Equal(t, "vip", one(t, ev, "f", evaluator.TypeString, user("v1"), nil).Value)
	disabled := one(t, ev, "f", evaluator.TypeString, user("v4", "plan", "beta"), nil)
	require.Equal(t, model.DisabledReason, disabled.Reason)
	require.False(t, disabled.HasValue)
}

// TestSegmentsWithoutIdentity: at environment level a segment still matches when its rules hold for an identity that has
// no traits. Rules that need a trait do not hold; rules that need its absence do. This is the engine's behaviour, kept
// as it is.
func TestSegmentsWithoutIdentity(t *testing.T) {
	for name, tc := range map[string]struct{ rules, want string }{
		"needs a trait":     {rule("ALL", []string{condition("EQUAL", "plan", "beta")}), "off"},
		"needs a trait set": {rule("ALL", []string{condition("IS_SET", "plan", "")}), "off"},
		"needs it not set":  {rule("ALL", []string{condition("IS_NOT_SET", "plan", "")}), "on"},
		"excludes a trait":  {rule("NONE", []string{condition("EQUAL", "plan", "beta")}), "on"},
	} {
		t.Run(name, func(t *testing.T) {
			ev, st := newEvaluator(t, nil, "src")
			deliver(t, st, "src", segmentsDoc("", segmentJSON(1, "s", 0, "on", tc.rules)))
			require.Equal(t, tc.want, one(t, ev, "f", evaluator.TypeString, nil, nil).Value)
		})
	}
}
