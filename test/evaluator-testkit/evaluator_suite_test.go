package evaluatortestkit_test

import (
	"errors"
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	evaluatortestkit "github.com/open-feature-forking/openfeature-relay/test/evaluator-testkit"
	"github.com/stretchr/testify/require"
)

// A2, BR-007: the JsonLogic evaluator passes the testbed's evaluator suite, scenario by scenario.
func TestJsonLogicEvaluatorPassesFlagdSuite(t *testing.T) {
	report := evaluatortestkit.Check(t, flagdeval.Factory{})
	require.Empty(t, report.Aborted)

	// the report holds one case for each scenario the inventory counts
	golden, err := os.ReadFile("testdata/contract_inventory.golden")
	require.NoError(t, err)
	match := regexp.MustCompile(`scenarios that run: (\d+)`).FindSubmatch(golden)
	require.NotNil(t, match)
	want, err := strconv.Atoi(string(match[1]))
	require.NoError(t, err)
	require.Len(t, report.Cases, want)

	report.Run(t)
}

// refusing is an evaluator factory that cannot create its evaluator.
type refusing struct{ flagdeval.Factory }

func (refusing) Create(evaluator.Settings) (evaluator.Evaluator, error) {
	return nil, errors.New("no backend")
}

// A scenario that fails is a failed case of the report with the reason, and the other scenarios still run.
func TestScenariosOfABrokenEvaluatorFail(t *testing.T) {
	report := evaluatortestkit.Check(t, refusing{})

	require.Empty(t, report.Aborted)
	require.True(t, report.Failed())
	require.NotEmpty(t, report.Cases)
	require.Equal(t, len(report.Cases), len(report.FailedCases()))
	for _, c := range report.Cases {
		require.Contains(t, c.Observed, "no backend")
	}
}

func TestMissingDeclarationsAbortTheRun(t *testing.T) {
	require.Contains(t, evaluatortestkit.Check(t, nil).Aborted, "the factory is nil")
}
