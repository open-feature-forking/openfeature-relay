package flagsmith_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
)

// benchDocument is an environment of the size of a busy project: 50 features, 10 of them multivariate, 20 segments that
// override five features each, and 1000 identity overrides in four groups.
func benchDocument() string {
	feature := func(i int) map[string]any {
		fs := map[string]any{
			"feature": map[string]any{"id": i, "name": fmt.Sprintf("feature-%d", i), "type": "STANDARD"},
			"enabled": i%7 != 0, "feature_state_value": fmt.Sprintf("value-%d", i), "django_id": i,
		}
		if i%5 == 0 {
			fs["multivariate_feature_state_values"] = []map[string]any{
				{"id": 10*i + 1, "multivariate_feature_option": map[string]any{"id": 10*i + 1, "key": "a", "value": "a"}, "percentage_allocation": 30},
				{"id": 10*i + 2, "multivariate_feature_option": map[string]any{"id": 10*i + 2, "key": "b", "value": "b"}, "percentage_allocation": 30},
			}
		}
		return fs
	}
	var states []map[string]any
	for i := 1; i <= 50; i++ {
		states = append(states, feature(i))
	}

	var segments []map[string]any
	for s := 1; s <= 20; s++ {
		var overrides []map[string]any
		for j := range 5 {
			f := feature((s*5+j)%50 + 1)
			f["feature_state_value"] = fmt.Sprintf("segment-%d", s)
			f["feature_segment"] = map[string]any{"priority": s}
			overrides = append(overrides, f)
		}
		cond := map[string]any{"operator": "EQUAL", "property_": "plan", "value": fmt.Sprintf("plan-%d", s%4)}
		switch s % 5 {
		case 1:
			cond = map[string]any{"operator": "REGEX", "property_": "email", "value": fmt.Sprintf(`@team%d\.example\.com$`, s)}
		case 2:
			cond = map[string]any{"operator": "GREATER_THAN", "property_": "age", "value": fmt.Sprint(20 + s)}
		case 3:
			cond = map[string]any{"operator": "PERCENTAGE_SPLIT", "property_": "", "value": "30"}
		}
		segments = append(segments, map[string]any{
			"id": s, "name": fmt.Sprintf("segment-%d", s), "feature_states": overrides,
			"rules": []map[string]any{{"type": "ALL", "conditions": []map[string]any{cond}}},
		})
	}

	var identities []map[string]any
	for i := range 1000 {
		f := feature(i%4 + 1)
		f["feature_state_value"] = fmt.Sprintf("identity-group-%d", i%4)
		identities = append(identities, map[string]any{
			"identifier": fmt.Sprintf("identity-%d", i), "identity_features": []map[string]any{f},
		})
	}

	raw, err := json.Marshal(map[string]any{
		"id": 1, "name": "bench", "api_key": "BENCH", "updated_at": "2026-01-01T00:00:00Z",
		"project":            map[string]any{"hide_disabled_flags": false, "segments": segments},
		"feature_states":     states,
		"identity_overrides": identities,
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func BenchmarkEvaluate(b *testing.B) {
	ev, st := newEvaluator(b, nil, "src")
	deliver(b, st, "src", benchDocument())
	identity := map[string]any{"targetingKey": "identity-500", "plan": "plan-2", "email": "a@team1.example.com", "age": 30.0}

	for name, req := range map[string]evaluator.Request{
		"single/identity":    {FlagKey: "feature-26", TypeHint: evaluator.TypeString, Context: identity},
		"single/environment": {FlagKey: "feature-26", TypeHint: evaluator.TypeString},
		"bulk/identity":      {Bulk: true, Context: identity},
		"bulk/environment":   {Bulk: true},
		"keys/identity":      {FlagKeys: []string{"feature-26", "feature-3", "missing"}, Context: identity},
		"keys/environment":   {FlagKeys: []string{"feature-26", "feature-3", "missing"}},
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			var dst []evaluator.Result
			for b.Loop() {
				var err error
				dst, _, err = ev.Evaluate(b.Context(), req, dst[:0])
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
