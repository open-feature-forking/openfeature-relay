package flagsmith_test

import (
	"crypto/md5"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
)

// percentage is Flagsmith's placement of an identity in a percentage split, written out from its definition: the MD5 of
// the ids joined with commas, repeated as many times as there are iterations, taken modulo 9999 and scaled onto
// [0, 100]. A result of exactly 100 is computed again with one more iteration, so that a split of 100% still has
// every identity inside and no split has one at 100.
func percentage(ids []string, iterations int) float64 {
	parts := make([]string, 0, len(ids)*iterations)
	for i := 0; i < len(ids)*iterations; i++ {
		parts = append(parts, ids[i%len(ids)])
	}
	sum := md5.Sum([]byte(strings.Join(parts, ",")))
	n := new(big.Int).SetBytes(sum[:])
	n.Mod(n, big.NewInt(9999))
	if v := float64(n.Int64()) / 9998.0 * 100.0; v != 100 {
		return v
	}
	return percentage(ids, iterations+1)
}

// TestPercentageVectors fixes the definition above with values that were computed once, so that an edit to it cannot
// go unnoticed.
func TestPercentageVectors(t *testing.T) {
	for _, tc := range []struct {
		ids  []string
		want float64
	}{
		{[]string{"7", "ENV_a"}, 61.722344468893773},
		{[]string{"7", "ENV_b"}, 8.4816963392678542},
		{[]string{"feature", "identity"}, 29.925985197039406},
		{[]string{"1", "ENV_user-0"}, 41.488297659531909},
		{[]string{"x"}, 18.243648729745949},
		{[]string{"", ""}, 51.200240048009604},
		// the hash of these ids is exactly 100 on the first iteration
		{[]string{"7", "ENV_u1395"}, 43.988797759551915},
		{[]string{"7", "ENV_u23095"}, 38.797759551910382},
		{[]string{"7", "ENV_u29503"}, 9.0218043608721743},
	} {
		require.InDelta(t, tc.want, percentage(tc.ids, 1), 1e-12, "%q", tc.ids)
	}
}

// multivariateDoc holds one multivariate feature, "exp" with the key 7: variant a for the first 20% of the hash space,
// variant b for the next 30%, and the control value for the rest.
const multivariateDoc = `{"api_key":"ENV","project":{"segments":[]},"feature_states":[{
	"feature":{"id":7,"name":"exp"},"enabled":true,"feature_state_value":"control","django_id":7,
	"multivariate_feature_state_values":[
		{"id":1,"multivariate_feature_option":{"id":1,"key":"a","value":"value-a"},"percentage_allocation":20},
		{"id":2,"multivariate_feature_option":{"id":2,"key":"b","value":"value-b"},"percentage_allocation":30}]}]}`

func expectedVariant(identifier string) string {
	switch p := percentage([]string{"7", "ENV_" + identifier}, 1); {
	case p <= 20:
		return "a"
	case p <= 50:
		return "b"
	}
	return "control"
}

// TestMultivariateFollowsHash checks the split a multivariate feature makes against the definition of the hash: the
// feature's key and the identity's key, which is the environment's key, an underscore and the identifier (UC-019 R1).
func TestMultivariateFollowsHash(t *testing.T) {
	ev, st := newEvaluator(t, nil, "src")
	deliver(t, st, "src", multivariateDoc)

	counts := map[string]int{}
	for i := range 3000 {
		id := fmt.Sprintf("u%d", i)
		r := one(t, ev, "exp", evaluator.TypeString, user(id), nil)
		require.Equal(t, expectedVariant(id), r.Variant, id)
		counts[r.Variant]++
		if r.Variant == "control" {
			require.Equal(t, "control", r.Value)
			require.Equal(t, model.StaticReason, r.Reason)
		} else {
			require.Equal(t, "value-"+r.Variant, r.Value)
			require.Equal(t, model.SplitReason, r.Reason)
		}
	}
	// 20% / 30% / 50% of 3000 identities, within a generous tolerance
	require.InDelta(t, 600, counts["a"], 120, "%v", counts)
	require.InDelta(t, 900, counts["b"], 120, "%v", counts)
	require.InDelta(t, 1500, counts["control"], 150, "%v", counts)
}

// TestHashOfExactlyOneHundred: an identity whose hash scales to exactly 100 is placed by the next iteration, not past
// the last variant. These identities were searched for, so that the case is not left to chance.
func TestHashOfExactlyOneHundred(t *testing.T) {
	ev, st := newEvaluator(t, nil, "src")
	deliver(t, st, "src", multivariateDoc)

	for id, variant := range map[string]string{"u1395": "b", "u23095": "b", "u29503": "a"} {
		require.Equal(t, variant, expectedVariant(id), id)
		r := one(t, ev, "exp", evaluator.TypeString, user(id), nil)
		require.Equal(t, variant, r.Variant, id)
	}
}

// TestNoIdentityNoSplit: without a targeting key, a multivariate feature keeps its environment value.
func TestNoIdentityNoSplit(t *testing.T) {
	ev, st := newEvaluator(t, nil, "src")
	deliver(t, st, "src", multivariateDoc)
	r := one(t, ev, "exp", evaluator.TypeString, nil, nil)
	require.Equal(t, "control", r.Value)
	require.Empty(t, r.Variant)
}

func percentageSegmentDoc(property string) string {
	return fmt.Sprintf(`{"api_key":"ENV","project":{"segments":[{"id":1,"name":"half","rules":[{"type":"ALL","conditions":[
		{"operator":"PERCENTAGE_SPLIT","property_":%q,"value":"50"}]}],
		"feature_states":[{"feature":{"id":1,"name":"f"},"enabled":true,"feature_state_value":"on","feature_segment":{"priority":0},"django_id":2}]}]},
		"feature_states":[{"feature":{"id":1,"name":"f"},"enabled":true,"feature_state_value":"off","django_id":1}]}`, property)
}

// TestPercentageSplitSegments: a segment's split places the identity by the segment's key and the identity's key, or by
// the value of the trait the condition names.
func TestPercentageSplitSegments(t *testing.T) {
	ev, st := newEvaluator(t, nil, "src")
	deliver(t, st, "src", percentageSegmentDoc(""))
	on := 0
	for i := range 500 {
		id := fmt.Sprintf("u%d", i)
		want := "off"
		if percentage([]string{"1", "ENV_" + id}, 1) <= 50 {
			want = "on"
			on++
		}
		require.Equal(t, want, one(t, ev, "f", evaluator.TypeString, user(id), nil).Value, id)
	}
	require.InDelta(t, 250, on, 60)

	deliver(t, st, "src", percentageSegmentDoc("bucket"))
	for i := range 500 {
		bucket := fmt.Sprintf("bucket-%d", i%50)
		want := "off"
		if percentage([]string{"1", bucket}, 1) <= 50 {
			want = "on"
		}
		// the trait decides, not the identity: identities sharing a bucket land together
		require.Equal(t, want, one(t, ev, "f", evaluator.TypeString, user(fmt.Sprintf("u%d", i), "bucket", bucket), nil).Value, bucket)
	}

	// no identity and no trait: the split matches nobody
	require.Equal(t, "off", one(t, ev, "f", evaluator.TypeString, nil, nil).Value)
}
