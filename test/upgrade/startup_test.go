package upgrade

import (
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// runs is the number of interleaved starts of each image.
const startupRuns = 10

// startupMedians starts the two images alternately, each timed from the start of the container to the first 200 on
// /readyz, and returns the median of each.
func startupMedians(t *testing.T, baseline, candidate string) (time.Duration, time.Duration) {
	t.Helper()
	measure := func(image string) time.Duration {
		began := time.Now()
		r := start(t, image, []string{"start", "--uri", "file:/data/flags.json"}, nil, map[string]string{"flags.json": flagsJSON})
		require.True(t, r.ready(60*time.Second), "%s did not become ready: %s", image, r.logs())
		took := time.Since(began)
		_, _ = docker(t.Context(), "rm", "-f", r.id)
		return took
	}
	var before, after []time.Duration
	for i := 0; i < startupRuns; i++ {
		before = append(before, measure(baseline))
		after = append(after, measure(candidate))
	}
	median := func(d []time.Duration) time.Duration {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return (d[len(d)/2-1] + d[len(d)/2]) / 2
	}
	return median(before), median(after)
}

// TestStartupTime is NFR-002 and BR-005: flagd-full becomes ready within 10% of the time today's flagd needs with the
// same source, measured over interleaved runs on the same machine. A miss is measured once more before it fails. The
// numbers go to the job summary.
func TestStartupTime(t *testing.T) {
	baseline, candidate := images(t)

	before, after := startupMedians(t, baseline, candidate)
	within := func(before, after time.Duration) bool { return float64(after) <= float64(before)*1.10 }
	summary := fmt.Sprintf("startup to /readyz, median of %d interleaved runs: baseline %v, candidate %v (%+.1f%%)",
		startupRuns, before.Round(time.Millisecond), after.Round(time.Millisecond), 100*(float64(after)/float64(before)-1))
	if !within(before, after) {
		t.Logf("a first measurement missed the 10%% bound: %s", summary)
		before, after = startupMedians(t, baseline, candidate)
		summary = fmt.Sprintf("startup to /readyz, median of %d interleaved runs (second measurement): baseline %v, candidate %v (%+.1f%%)",
			startupRuns, before.Round(time.Millisecond), after.Round(time.Millisecond), 100*(float64(after)/float64(before)-1))
	}
	t.Log(summary)
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil { //nolint:gosec // the job summary
			fmt.Fprintf(f, "### Startup time (NFR-002)\n\n%s\n\n", summary)
			_ = f.Close()
		}
	}
	require.True(t, within(before, after), summary)
}
