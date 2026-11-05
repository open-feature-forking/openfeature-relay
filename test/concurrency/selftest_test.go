package concurrency

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCheckerSelfTest runs k6/selftest.js: every check of the oracle rejects its synthetic wrong answer and accepts the
// right one. A case that silently did not run fails here too.
func TestCheckerSelfTest(t *testing.T) {
	requireLoad(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	run := runK6(ctx, t, nil, "selftest.js", nil)

	passed := 0
	for _, l := range strings.Split(run.Output, "\n") {
		if rest, ok := strings.CutPrefix(l, "SELFTEST ok "); ok {
			passed++
			t.Log("ok   " + rest)
		} else if strings.HasPrefix(l, "SELFTEST ") {
			t.Error(l)
		}
	}
	require.Zero(t, run.ExitCode, run.Output)
	require.Equal(t, 22, passed, "the self-test ran every case:\n%s", run.Output)
}
