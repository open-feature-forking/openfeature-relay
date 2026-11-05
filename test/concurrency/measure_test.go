package concurrency

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// cpuSeconds reads the user and system CPU time of a process from /proc.
func cpuSeconds(t *testing.T, pid int) float64 {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	require.NoError(t, err)
	rest := string(raw[strings.LastIndexByte(string(raw), ')')+2:])
	f := strings.Fields(rest) // field 3 onward: utime is the 12th, stime the 13th of these
	utime, _ := strconv.ParseFloat(f[11], 64)
	stime, _ := strconv.ParseFloat(f[12], 64)
	var tick float64 = 100 // USER_HZ
	return (utime + stime) / tick
}

// TestMeasureRates sets the request rates of the profiles: it runs the request scenarios at RELAY_MEASURE_RATES (requests
// per second of each of ofrep-single and rpc-unary, bulk at a tenth) against the race build and logs flagd's CPU cores
// and what k6 saw at each. It asserts nothing about the rates; run it where the rates are to be set.
func TestMeasureRates(t *testing.T) {
	requireLoad(t)
	if os.Getenv("RELAY_MEASURE_RATES") == "" {
		t.Skip("RELAY_MEASURE_RATES is not set, e.g. 20,50,100,200")
	}
	for _, field := range strings.Split(os.Getenv("RELAY_MEASURE_RATES"), ",") {
		rate := field
		t.Run("rate-"+rate, func(t *testing.T) {
			p := startFlagd(t)
			before := cpuSeconds(t, p.cmd.Process.Pid)
			started := time.Now()
			t0 := started.Add(startup)
			n := 6
			ledger := &Ledger{T0: t0, P: period, D: bound, N: n}
			c := &Conductor{Ledger: ledger, Clock: RealClock(), Eps: eps, Path: p.Flags, SyncAddr: p.SyncAddr()}
			done := make(chan *Report, 1)
			go func() { rep, _ := c.Run(t.Context()); done <- rep }()
			end := ledger.Write(n).Add(bound)
			run := runK6(t.Context(), t, p, "load.js", map[string]string{
				"PROFILE": "measure", "RATE": rate, "T0": fmt.Sprint(t0.UnixMilli()), "P": fmt.Sprint(period.Milliseconds()),
				"D": fmt.Sprint(bound.Milliseconds()), "EPS": fmt.Sprint(eps.Milliseconds()), "N": fmt.Sprint(n),
				"END": fmt.Sprint(end.UnixMilli()), "CHURN_MS": "6000",
			})
			rep := <-done
			cores := (cpuSeconds(t, p.cmd.Process.Pid) - before) / time.Since(started).Seconds()
			t.Logf("rate %s/s: flagd used %.2f cores on average; k6 exit %d; apply latency %s", rate, cores, run.ExitCode, rep.LatencySummary())
			for _, line := range strings.Split(run.Output, "\n") {
				if strings.Contains(line, "http_req_duration") || strings.Contains(line, "grpc_req_duration") || strings.Contains(line, "wrong_answer") || strings.Contains(line, "stale_answer") || strings.Contains(line, "dropped_iterations") {
					t.Log(strings.TrimSpace(line))
				}
			}
		})
	}
}
