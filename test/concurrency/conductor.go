package concurrency

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"buf.build/gen/go/open-feature/flagd/grpc/go/flagd/sync/v1/syncv1grpc"
	syncv1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Clock is the conductor's view of time. Mono is a monotonic reading, so a wall clock step shows as the two drifting
// apart.
type Clock interface {
	Now() time.Time
	Mono() time.Duration
	SleepUntil(ctx context.Context, t time.Time) error
}

type realClock struct{ start time.Time }

// RealClock is the machine's clock.
func RealClock() Clock { return realClock{start: time.Now()} }

func (realClock) Now() time.Time { return time.Now().Round(0) }

func (c realClock) Mono() time.Duration { return time.Since(c.start) }

func (realClock) SleepUntil(ctx context.Context, t time.Time) error {
	timer := time.NewTimer(time.Until(t))
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Late is a generation the conductor's own sync stream received after the apply bound, or never.
type Late struct {
	Gen      int
	Lateness time.Duration // behind w_g + D; zero with Never
	Never    bool
}

func (l Late) String() string {
	if l.Never {
		return fmt.Sprintf("generation %d never arrived", l.Gen)
	}
	return fmt.Sprintf("generation %d arrived %s after its apply bound", l.Gen, l.Lateness.Round(time.Millisecond))
}

// Ledger records when each generation reached the conductor's stream, against the schedule w_g = T0 + g·P (Q3).
// Generation 0 is the initial document, confirmed before T0, and is never late.
type Ledger struct {
	T0   time.Time
	P, D time.Duration
	N    int // generations written after generation 0

	mu      sync.Mutex
	arrived map[int]time.Time
}

// Write returns w_g.
func (l *Ledger) Write(g int) time.Time { return l.T0.Add(time.Duration(g) * l.P) }

// Record notes the first arrival of generation g.
func (l *Ledger) Record(g int, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.arrived == nil {
		l.arrived = map[int]time.Time{}
	}
	if _, ok := l.arrived[g]; !ok {
		l.arrived[g] = at
	}
}

// Arrival returns when generation g arrived.
func (l *Ledger) Arrival(g int) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	at, ok := l.arrived[g]
	return at, ok
}

// Late lists the generations of 1..N that arrived after w_g + D. A generation that never arrived is measured against
// the first later generation that did, or reported as never.
func (l *Ledger) Late() []Late {
	l.mu.Lock()
	defer l.mu.Unlock()
	var late []Late
	for g := 1; g <= l.N; g++ {
		bound := l.Write(g).Add(l.D)
		at, ok := l.arrived[g]
		if !ok {
			var next time.Time
			for h := g + 1; h <= l.N; h++ {
				if t, seen := l.arrived[h]; seen {
					next = t
					break
				}
			}
			if next.IsZero() {
				late = append(late, Late{Gen: g, Never: true})
			} else {
				late = append(late, Late{Gen: g, Lateness: next.Sub(bound)})
			}
			continue
		}
		if at.After(bound) {
			late = append(late, Late{Gen: g, Lateness: at.Sub(bound)})
		}
	}
	return late
}

// ClockStepped reports a wall clock step: wall time since the start and the monotonic reading since the start differ
// by more than eps.
func ClockStepped(c Clock, start time.Time, startMono, eps time.Duration) error {
	wall, mono := c.Now().Sub(start), c.Mono()-startMono
	if drift := wall - mono; drift > eps || drift < -eps {
		return fmt.Errorf("clock stepped: wall time advanced %s while the monotonic clock advanced %s", wall, mono)
	}
	return nil
}

// WriteAtomic writes content to path through a temporary file in the same directory and a rename over path, as a
// Kubernetes volume update does.
func WriteAtomic(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".flags-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(content)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Report is what a conductor run found.
type Report struct {
	Late         []Late
	ClockStep    error
	ReadyFailed  int
	StreamError  error
	LatencyMax   time.Duration // the longest apply latency, measured from w_g
	LatencyByGen map[int]time.Duration
}

// Conductor changes the flags on a schedule (Q2 (a)), holds its own sync stream to see when each generation is served,
// watches the clock and polls readiness.
type Conductor struct {
	Ledger *Ledger
	Clock  Clock
	Eps    time.Duration
	Path   string
	Skip   map[int]bool // generations whose write is left out (the negative test)

	SyncAddr string
	ReadyURL string
}

// Run writes generations 1..N and returns once the last one is due plus D. The flag file must hold generation 0.
func (c *Conductor) Run(ctx context.Context) (*Report, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	rep := &Report{LatencyByGen: map[int]time.Duration{}}
	var mu sync.Mutex
	streamUp := make(chan struct{})
	streamDone := make(chan error, 1)

	if c.SyncAddr != "" {
		go func() { streamDone <- c.subscribe(ctx, streamUp) }()
		select {
		case <-streamUp:
		case err := <-streamDone:
			return nil, fmt.Errorf("the conductor's sync stream: %w", err)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	start, startMono := c.Clock.Now(), c.Clock.Mono()
	watch := make(chan struct{})
	go func() {
		defer close(watch)
		for tick := start.Add(time.Second); ; tick = tick.Add(time.Second) {
			if c.Clock.SleepUntil(ctx, tick) != nil {
				return
			}
			if err := ClockStepped(c.Clock, start, startMono, c.Eps); err != nil {
				mu.Lock()
				if rep.ClockStep == nil {
					rep.ClockStep = err
				}
				mu.Unlock()
			}
			if c.ReadyURL != "" && !ready(ctx, c.ReadyURL) && ctx.Err() == nil {
				mu.Lock()
				rep.ReadyFailed++
				mu.Unlock()
			}
		}
	}()

	l := c.Ledger
	for g := 1; g <= l.N; g++ {
		if err := c.Clock.SleepUntil(ctx, l.Write(g)); err != nil {
			return nil, err
		}
		if c.Skip[g] {
			continue
		}
		if err := WriteAtomic(c.Path, Document(g)); err != nil {
			return nil, fmt.Errorf("writing generation %d: %w", g, err)
		}
	}
	if err := c.Clock.SleepUntil(ctx, l.Write(l.N).Add(l.D+time.Second)); err != nil {
		return nil, err
	}
	cancel()
	<-watch
	if c.SyncAddr != "" {
		if err := <-streamDone; err != nil && !isCanceled(err) {
			rep.StreamError = err
		}
	}

	mu.Lock()
	defer mu.Unlock()
	rep.Late = l.Late()
	for g := 1; g <= l.N; g++ {
		if at, ok := l.Arrival(g); ok {
			d := at.Sub(l.Write(g))
			rep.LatencyByGen[g] = d
			rep.LatencyMax = max(rep.LatencyMax, d)
		}
	}
	return rep, nil
}

func isCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled
}

func ready(ctx context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// subscribe holds a sync stream and records when each generation arrives. up closes once the first payload arrived.
func (c *Conductor) subscribe(ctx context.Context, up chan<- struct{}) error {
	conn, err := grpc.NewClient(c.SyncAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	stream, err := syncv1grpc.NewFlagSyncServiceClient(conn).SyncFlags(ctx, &syncv1.SyncFlagsRequest{})
	if err != nil {
		return err
	}
	first := true
	for {
		resp, err := stream.Recv()
		if err != nil {
			return err
		}
		at := c.Clock.Now()
		g, err := Generation([]byte(resp.GetFlagConfiguration()))
		if err != nil {
			return fmt.Errorf("parsing a sync payload: %w", err)
		}
		c.Ledger.Record(g, at)
		if first {
			first = false
			close(up)
		}
	}
}

// LateGenerations returns the generations of late, in order.
func LateGenerations(late []Late) []int {
	gens := make([]int, 0, len(late))
	for _, l := range late {
		gens = append(gens, l.Gen)
	}
	sort.Ints(gens)
	return gens
}

// LatencySummary describes the apply latencies of a run: the fastest, the median and the slowest.
func (r *Report) LatencySummary() string {
	var ds []time.Duration
	for _, d := range r.LatencyByGen {
		ds = append(ds, d)
	}
	if len(ds) == 0 {
		return "no generation arrived"
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	return fmt.Sprintf("min %s, median %s, max %s (%d of the generations arrived)",
		ds[0].Round(time.Millisecond), ds[len(ds)/2].Round(time.Millisecond), ds[len(ds)-1].Round(time.Millisecond), len(ds))
}
