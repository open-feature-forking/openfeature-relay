package concurrency

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func execOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// fakeClock advances only when told to.
type fakeClock struct {
	wall time.Time
	mono time.Duration
}

func (c *fakeClock) Now() time.Time                                  { return c.wall }
func (c *fakeClock) Mono() time.Duration                             { return c.mono }
func (c *fakeClock) SleepUntil(_ context.Context, _ time.Time) error { return nil }

func (c *fakeClock) advance(d time.Duration) { c.wall, c.mono = c.wall.Add(d), c.mono+d }

func TestLedgerReportsApplyLateAgainstTheSchedule(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := &Ledger{T0: t0, P: 3 * time.Second, D: time.Second, N: 5}
	at := func(g int, after time.Duration) time.Time { return l.Write(g).Add(after) }

	l.Record(0, t0)
	l.Record(1, at(1, 100*time.Millisecond))
	l.Record(2, at(2, time.Second))           // exactly on the bound: on time
	l.Record(3, at(3, 1500*time.Millisecond)) // late by 500 ms
	l.Record(3, at(3, 5*time.Second))         // only the first arrival counts
	l.Record(5, at(5, 10*time.Millisecond))   // generation 4 never arrived, 5 did

	late := l.Late()
	require.Equal(t, []int{3, 4}, LateGenerations(late))
	require.Equal(t, 500*time.Millisecond, late[0].Lateness)
	require.False(t, late[0].Never)
	// generation 4 is measured against the next arrival, 5: w5 + 10ms - (w4 + D) = P + 10ms - D
	require.Equal(t, 2*time.Second+10*time.Millisecond, late[1].Lateness)
}

func TestLedgerGenerationNeverArrived(t *testing.T) {
	l := &Ledger{T0: time.Unix(0, 0), P: time.Second, D: 500 * time.Millisecond, N: 2}
	l.Record(1, l.Write(1))
	late := l.Late()
	require.Equal(t, []int{2}, LateGenerations(late))
	require.True(t, late[0].Never)
}

func TestClockSteppedWithAFakeClock(t *testing.T) {
	c := &fakeClock{wall: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	start, startMono := c.Now(), c.Mono()

	c.advance(time.Second)
	require.NoError(t, ClockStepped(c, start, startMono, 100*time.Millisecond))

	c.wall = c.wall.Add(2 * time.Second) // NTP steps the wall clock forward
	require.ErrorContains(t, ClockStepped(c, start, startMono, 100*time.Millisecond), "clock stepped")

	c.wall = c.wall.Add(-4 * time.Second) // and back
	require.ErrorContains(t, ClockStepped(c, start, startMono, 100*time.Millisecond), "clock stepped")
}

func TestWriteAtomicReplacesByRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flags.json")
	require.NoError(t, WriteAtomic(path, Document(0)))
	before, err := os.Stat(path)
	require.NoError(t, err)

	require.NoError(t, WriteAtomic(path, Document(1)))
	after, err := os.Stat(path)
	require.NoError(t, err)
	require.False(t, os.SameFile(before, after), "a rename puts a new file over the path")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	g, err := Generation(got)
	require.NoError(t, err)
	require.Equal(t, 1, g)
	left, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".flags-*"))
	require.NoError(t, err)
	require.Empty(t, left, "no temporary file is left behind")
}

func TestConductorWritesOnTheSchedule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flags.json")
	require.NoError(t, WriteAtomic(path, Document(0)))
	l := &Ledger{T0: time.Now().Add(50 * time.Millisecond), P: 100 * time.Millisecond, D: 100 * time.Millisecond, N: 3}
	c := &Conductor{Ledger: l, Clock: RealClock(), Eps: 50 * time.Millisecond, Path: path, Skip: map[int]bool{2: true}}

	rep, err := c.Run(context.Background())
	require.NoError(t, err)
	require.NoError(t, rep.ClockStep)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	g, err := Generation(got)
	require.NoError(t, err)
	require.Equal(t, 3, g, "generation 2 was skipped and generation 3 written")
}
