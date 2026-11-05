package concurrency

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestStreamSpike (Q1) proves k6 core can hold a flag sync stream to the end of a run, with its final check on a timer,
// close one with client.close() and reconnect, and open an event stream on each evaluation service.
func TestStreamSpike(t *testing.T) {
	requireLoad(t)
	p := startFlagd(t)
	const seconds = 20
	t0 := time.Now().Add(6 * time.Second)
	ledger := &Ledger{T0: t0, P: period, D: bound, N: 5}
	c := &Conductor{Ledger: ledger, Clock: RealClock(), Eps: eps, Path: p.Flags, SyncAddr: p.SyncAddr(), ReadyURL: p.ReadyURL()}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	type result struct {
		rep *Report
		err error
	}
	done := make(chan result, 1)
	go func() { rep, err := c.Run(ctx); done <- result{rep, err} }()

	run := runK6(ctx, t, p, "spike.js", map[string]string{
		"SPIKE_SECONDS": fmt.Sprint(seconds), "T0": fmt.Sprint(t0.UnixMilli()), "P": fmt.Sprint(period.Milliseconds()),
		"D": fmt.Sprint(bound.Milliseconds()),
	})
	t.Log("k6 output:\n" + run.Output)
	require.Zero(t, run.ExitCode, run.Output)
	res := <-done
	require.NoError(t, res.err)
	require.Empty(t, res.rep.Late)

	lines := spikeLines(t, run.Output)

	final := only(t, lines, "held", "final")
	require.GreaterOrEqual(t, final["payloads"], float64(2), "the held stream kept delivering")
	require.GreaterOrEqual(t, final["last"], final["expectedAtLeast"], "the held stream is at the generation due, on the timer")
	// client.close() fires error ("canceled by client") and then end; the checker must keep a planned close out of
	// stream_error.
	require.Equal(t, []any{"error", "end"}, only(t, lines, "churn", "closed", 0)["events"])
	require.Equal(t, []any{"error", "end", "error", "end"}, only(t, lines, "churn", "closed", 1)["events"])
	require.Equal(t, float64(0), only(t, lines, "churn", "fetch")["status"], "FetchAllFlags answers OK (status 0)")

	seen := only(t, lines, "events", "final")["seen"].(map[string]any)
	require.Len(t, seen, 3)
	for service, types := range seen {
		require.Equal(t, "provider_ready", types.([]any)[0], service)
		require.Contains(t, types, "configuration_change", service)
	}
}

func spikeLines(t *testing.T, output string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, l := range strings.Split(output, "\n") {
		if rest, ok := strings.CutPrefix(l, "SPIKE "); ok {
			m := map[string]any{}
			require.NoError(t, json.Unmarshal([]byte(rest), &m), l)
			lines = append(lines, m)
		}
	}
	return lines
}

// only returns the one line of a scenario and event; round selects among several "closed" lines.
func only(t *testing.T, lines []map[string]any, scenario, what string, round ...int) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, l := range lines {
		if l["scenario"] == scenario && l["what"] == what && (len(round) == 0 || l["round"] == float64(round[0])) {
			found = append(found, l)
		}
	}
	require.Len(t, found, 1, "%s %s %v", scenario, what, round)
	return found[0]
}
