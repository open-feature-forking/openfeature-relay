package evaluatortestkit_test

import (
	"testing"

	evaluatortestkit "github.com/open-feature-forking/openfeature-relay/test/evaluator-testkit"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
)

// A2, BR-007: the JsonLogic evaluator passes the testbed's evaluator suite.
func TestJsonLogicEvaluatorPassesFlagdSuite(t *testing.T) {
	evaluatortestkit.Run(t, flagdeval.Factory{})
}
