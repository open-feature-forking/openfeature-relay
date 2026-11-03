package evaluatortest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The runner records no case that passes, so the names of its cases are pinned by the broken evaluators of
// TestContractReportsExpectedAndObserved, which each fail under a named case. What follows pins the behaviour the
// contract changes on purpose: an optional sample that is absent is not checked, and the run still passes.
// The case list itself starts with the commit that introduces it.
func TestOptionalSamplesPassSilentlyWhenAbsent(t *testing.T) {
	e := &reference{}
	samples := samplesFor(e)
	samples.Disabled = ""
	samples.AbsentDefault = ""
	samples.Attribute = nil
	samples.Paired = nil
	samples.Shared = nil

	tb := &fakeTB{}
	Run(tb, e, samples)

	require.Empty(t, tb.errors, "an evaluator with the optional samples left out passes, whatever it does for them")
}
