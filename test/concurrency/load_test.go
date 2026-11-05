package concurrency

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// startup is how long k6's container takes to start before T0 may begin the schedule.
const startup = 8 * time.Second

type loadRun struct {
	k6    k6Run
	rep   *Report
	flagd *flagdProc
}

// runLoad starts flagd-minimal, runs the conductor for n generations (skipping those in skip) and k6's load.js beside it,
// and returns what both saw. It stops flagd and returns its exit error.
func runLoad(t *testing.T, n int, skip map[int]bool, env map[string]string) *loadRun {
	t.Helper()
	p := startFlagd(t)
	t0 := time.Now().Add(startup)
	ledger := &Ledger{T0: t0, P: period, D: bound, N: n}
	c := &Conductor{Ledger: ledger, Clock: RealClock(), Eps: eps, Path: p.Flags, Skip: skip, SyncAddr: p.SyncAddr(), ReadyURL: p.ReadyURL()}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	type result struct {
		rep *Report
		err error
	}
	done := make(chan result, 1)
	go func() { rep, err := c.Run(ctx); done <- result{rep, err} }()

	end := ledger.Write(n).Add(bound)
	args := map[string]string{
		"PROFILE": profile(), "T0": fmt.Sprint(t0.UnixMilli()), "P": fmt.Sprint(period.Milliseconds()),
		"D": fmt.Sprint(bound.Milliseconds()), "EPS": fmt.Sprint(eps.Milliseconds()), "N": fmt.Sprint(n),
		"END": fmt.Sprint(end.UnixMilli()), "CHURN_MS": "6000",
	}
	for k, v := range env {
		args[k] = v
	}
	run := runK6(ctx, t, p, "load.js", args)
	res := <-done
	require.NoError(t, res.err)
	require.NoError(t, p.stop(t), "flagd exits cleanly on SIGTERM:\n%s", p.stderr)
	require.NotContains(t, p.stderr.String(), "WARNING: DATA RACE", "flagd's stderr")
	return &loadRun{k6: run, rep: res.rep, flagd: p}
}

// metric reads a counter's total from k6's end-of-test summary.
func metric(t *testing.T, output, name string) int {
	t.Helper()
	m := regexp.MustCompile(`(?m)^\s*` + name + `\.*:\s+(\d+)`).FindStringSubmatch(output)
	require.NotNil(t, m, "k6's summary has no %s:\n%s", name, output)
	n, err := strconv.Atoi(m[1])
	require.NoError(t, err)
	return n
}

func generations() int {
	if os.Getenv("RELAY_LOAD") == "full" {
		return 140 // about 10 minutes
	}
	return 13 // about 60 seconds
}

// profile is the k6 profile of the run: RELAY_LOAD names it, and the harness's own tests use "one".
func profile() string {
	if p := os.Getenv("RELAY_LOAD"); p == "smoke" || p == "full" {
		return p
	}
	return "one"
}

// TestConcurrency (NFR-017) runs the load against the race build: k6's thresholds hold, every generation reached the
// conductor's stream inside the apply bound, the clock never stepped, flagd stayed ready and shows no data race.
func TestConcurrency(t *testing.T) {
	requireLoad(t)
	r := runLoad(t, generations(), nil, nil)
	t.Log("k6 output:\n" + r.k6.Output)
	t.Logf("apply latency over %d generations: %s", generations(), r.rep.LatencySummary())

	require.Zero(t, r.k6.ExitCode, "k6 thresholds:\n%s", r.k6.Output)
	require.Empty(t, r.rep.Late, "apply_late (slow, not wrong): %v", r.rep.Late)
	require.NoError(t, r.rep.ClockStep)
	require.NoError(t, r.rep.StreamError)
	require.Zero(t, r.rep.ReadyFailed, "/readyz answered other than 200")
	require.Positive(t, metric(t, r.k6.Output, "answers_checked"))
}

// TestLoadDetectsStaleFlags: the conductor leaves one write out, so flagd keeps serving the generation before it. k6 has
// to see stale answers, and the conductor reports the generation as late.
func TestLoadDetectsStaleFlags(t *testing.T) {
	requireLoad(t)
	r := runLoad(t, 6, map[int]bool{3: true}, map[string]string{"PROFILE": "one"})
	t.Log("k6 output:\n" + r.k6.Output)

	require.Equal(t, 99, r.k6.ExitCode, "k6 fails its thresholds:\n%s", r.k6.Output)
	require.Positive(t, metric(t, r.k6.Output, "stale_answer"))
	require.Equal(t, []int{3}, LateGenerations(r.rep.Late), "apply_late names the generation not written: %v", r.rep.Late)
}

// TestRaceBuildInEffect: the binary under test was built with the race detector.
func TestRaceBuildInEffect(t *testing.T) {
	requireLoad(t)
	out, err := execOutput("go", "version", "-m", flagdBinary)
	require.NoError(t, err)
	require.True(t, strings.Contains(out, "-race=true"), "go version -m:\n%s", out)
}
