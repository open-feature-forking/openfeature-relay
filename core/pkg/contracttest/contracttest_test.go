package contracttest_test

import (
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/contracttest"
	"github.com/stretchr/testify/require"
)

func TestAbortedRunIsNeverPassed(t *testing.T) {
	r := contracttest.Abort("test input %s is missing", "Inputs.Passed")

	require.True(t, r.Failed())
	require.Empty(t, r.Cases)
	require.Contains(t, r.Aborted, "Inputs.Passed")
}

func TestFailedCaseFailsRun(t *testing.T) {
	r := contracttest.Report{Cases: []contracttest.Case{
		contracttest.Pass("a", "a works"),
		contracttest.Check("b", "b works", "b was broken"),
		contracttest.NotApplicable("c", "Without: listen"),
	}}

	require.True(t, r.Failed())
	require.Equal(t, []string{"b"}, r.FailedCases())
}

func TestNotApplicableCaseDoesNotFailRun(t *testing.T) {
	r := contracttest.Report{Cases: []contracttest.Case{
		contracttest.Pass("a", "a works"),
		contracttest.NotApplicable("c", "Without: listen"),
	}}

	require.False(t, r.Failed())
	require.Empty(t, r.FailedCases())
}

func TestNeedNamesTheCasesOfAMissingInput(t *testing.T) {
	require.Empty(t, contracttest.Need("Inputs.Passed", true, "one"))

	msg := contracttest.Need("Inputs.Passed", false, "one", "two")
	require.Equal(t, `test input Inputs.Passed is missing, needed by the cases "one", "two"`, msg)

	joined := contracttest.Missing(msg, contracttest.Need("Inputs.Answered", true, "x"),
		contracttest.Need("Inputs.Answered", false, "three"))
	require.Equal(t, 2, strings.Count(joined, "is missing"))
	require.Contains(t, joined, "; ")
}

type feature string

func TestValidateWithout(t *testing.T) {
	supplied := map[feature]bool{"documents": true, "listen": false}

	require.NoError(t, contracttest.ValidateWithout([]feature{"listen"}, supplied))
	require.NoError(t, contracttest.ValidateWithout(nil, supplied))

	err := contracttest.ValidateWithout([]feature{"documents"}, supplied)
	require.ErrorContains(t, err, `"documents" is both set and named`)

	err = contracttest.ValidateWithout([]feature{"nope"}, supplied)
	require.ErrorContains(t, err, `expected Without to name features of the contract: [documents listen]`)
	require.ErrorContains(t, err, `observed: "nope"`)

	err = contracttest.ValidateWithout([]feature{"documents", "nope"}, supplied)
	require.ErrorContains(t, err, "both set and named")
	require.ErrorContains(t, err, `observed: "nope"`)
}

func TestRunReportsOneSubtestPerCase(t *testing.T) {
	ran := 0
	for _, name := range []string{"first", "second"} {
		ok := t.Run("report of "+name, func(t *testing.T) {
			contracttest.Report{Cases: []contracttest.Case{
				contracttest.Pass(name, "works"),
				contracttest.NotApplicable(name+" optional", "Without: listen"),
			}}.Run(t)
		})
		require.True(t, ok)
		ran++
	}
	require.Equal(t, 2, ran)
}
