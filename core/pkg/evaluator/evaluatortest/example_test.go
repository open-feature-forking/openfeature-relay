package evaluatortest_test

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/evaluatortest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// extensionEvaluatorFactory is the Extension Author's own evaluator factory.
var extensionEvaluatorFactory evaluator.Factory

func ExampleRun() {
	_ = func(t *testing.T) {
		evaluatortest.Run(t, extensionEvaluatorFactory, evaluatortest.Samples{
			// deliver the sample flags, in the format of the evaluator
			Setup: func(st *store.Store) error { return nil },
			Static: []evaluatortest.StaticSample{
				{Key: "static-bool", Hint: evaluator.TypeBool, Value: true},
			},
			// the evaluator has no disabled flag, no flag without a value, no attributes and no shared flag set
			Without: []evaluatortest.Feature{
				evaluatortest.FeatureDisabled, evaluatortest.FeatureAbsentDefault, evaluatortest.FeatureAttribute,
				evaluatortest.FeaturePaired, evaluatortest.FeatureShared,
			},
		})
	}
}
