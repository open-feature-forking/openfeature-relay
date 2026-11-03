package telemetrytest_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry/telemetrytest"
)

// backend is an in-memory telemetry backend that can stall.
type backend struct {
	mu      sync.Mutex
	gate    chan struct{} // closed when the backend accepts data; replaced while it stalls
	spans   []string
	metrics []string
}

func newBackend() *backend {
	gate := make(chan struct{})
	close(gate)
	return &backend{gate: gate}
}

func (b *backend) stall(testing.TB) func() {
	b.mu.Lock()
	gate := make(chan struct{})
	b.gate = gate
	b.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }
}

func (b *backend) export(ctx context.Context, spans, metrics []string) error {
	b.mu.Lock()
	gate := b.gate
	b.mu.Unlock()
	select {
	case <-gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.spans = append(b.spans, spans...)
	b.metrics = append(b.metrics, metrics...)
	return nil
}

func (b *backend) observed(testing.TB) telemetrytest.Observation {
	b.mu.Lock()
	defer b.mu.Unlock()
	return telemetrytest.Observation{Spans: slices.Clone(b.spans), Metrics: slices.Clone(b.metrics)}
}

type bug int

const (
	bugNone bug = iota
	bugBlocksRequests
	bugChangesResponse
	bugIgnoresShutdownContext
	bugDropsOnShutdown
	bugAcceptsInvalid
	bugVagueError
	bugNotConcurrencySafe
	bugNilProvider
)

const validEndpoint, invalidEndpoint = "mem", "bogus://endpoint"

type stubFactory struct {
	typ     string
	bug     bug
	backend *backend
}

func (f stubFactory) Type() string { return f.typ }

func (f stubFactory) Create(_ context.Context, s telemetry.Settings) (telemetry.Provider, error) {
	if f.bug == bugNilProvider {
		return nil, nil
	}
	if endpoint, _ := s.Config["endpoint"].(string); endpoint != validEndpoint && f.bug != bugAcceptsInvalid {
		if f.bug == bugVagueError {
			return nil, errors.New("invalid configuration")
		}
		return nil, fmt.Errorf("invalid endpoint %q", endpoint)
	}
	return &stubProvider{bug: f.bug, backend: f.backend}, nil
}

// stubProvider holds what it records in memory and exports it on Shutdown.
type stubProvider struct {
	bug     bug
	backend *backend
	mu      sync.Mutex
	spans   []string
	metrics []string
	busy    atomic.Bool
}

func (p *stubProvider) held(span, metric string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if span != "" {
		p.spans = append(p.spans, span)
	}
	if metric != "" {
		p.metrics = append(p.metrics, metric)
	}
}

func (p *stubProvider) Tracer(string) telemetry.Tracer { return stubTracer{p} }

func (p *stubProvider) Meter(string) telemetry.Meter { return stubMeter{p} }

func (p *stubProvider) HTTPServerSpans(h http.Handler, operation string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.bug == bugChangesResponse {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.ServeHTTP(w, r)
		p.held(operation, "")
	})
}

func (p *stubProvider) HTTPServerMetrics(h http.Handler, _ telemetry.HTTPMetricsOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		if p.bug == bugBlocksRequests {
			_ = p.backend.export(context.Background(), nil, []string{"http.server.request.duration"})
			return
		}
		p.held("", "http.server.request.duration")
	})
}

func (p *stubProvider) Shutdown(ctx context.Context) error {
	if p.bug == bugDropsOnShutdown {
		return nil
	}
	if p.bug == bugIgnoresShutdownContext {
		ctx = context.Background()
	}
	p.mu.Lock()
	spans, metrics := p.spans, p.metrics
	p.spans, p.metrics = nil, nil
	p.mu.Unlock()
	return p.backend.export(ctx, spans, metrics)
}

type stubTracer struct{ p *stubProvider }

func (t stubTracer) Start(ctx context.Context, name string, _ ...telemetry.SpanOption) (context.Context, telemetry.Span) {
	return ctx, stubSpan{p: t.p, name: name}
}

type stubSpan struct {
	p    *stubProvider
	name string
}

func (stubSpan) SetAttributes(...telemetry.Attribute) {}
func (stubSpan) RecordError(error)                    {}
func (stubSpan) SetError(string)                      {}
func (s stubSpan) End()                               { s.p.held(s.name, "") }

type stubMeter struct{ p *stubProvider }

func (m stubMeter) Int64Counter(name string, _ ...telemetry.InstrumentOption) telemetry.Int64Counter {
	return stubInstrument{p: m.p, name: name}
}

func (m stubMeter) Int64UpDownCounter(name string, _ ...telemetry.InstrumentOption) telemetry.Int64UpDownCounter {
	return stubInstrument{p: m.p, name: name}
}

func (m stubMeter) Float64Histogram(name string, _ ...telemetry.InstrumentOption) telemetry.Float64Histogram {
	return stubInstrument{p: m.p, name: name}
}

type stubInstrument struct {
	p    *stubProvider
	name string
}

func (i stubInstrument) Add(context.Context, int64, ...telemetry.Attribute) {
	if i.p.bug == bugNotConcurrencySafe {
		if !i.p.busy.CompareAndSwap(false, true) {
			panic("concurrent use")
		}
		time.Sleep(50 * time.Microsecond)
		i.p.busy.Store(false)
	}
	i.p.held("", i.name)
}

func (i stubInstrument) Record(context.Context, float64, ...telemetry.Attribute) { i.p.held("", i.name) }

func setup(b bug) (stubFactory, telemetrytest.Inputs) {
	be := newBackend()
	return stubFactory{typ: "stub", bug: b, backend: be}, telemetrytest.Inputs{
		Config:        map[string]any{"endpoint": validEndpoint},
		InvalidConfig: map[string]any{"endpoint": invalidEndpoint},
		FaultyValue:   invalidEndpoint,
		StallBackend:  be.stall,
		Observed:      be.observed,
		Timeout:       2 * time.Second,
	}
}

func TestContractOnPassingStub(t *testing.T) {
	f, in := setup(bugNone)
	telemetrytest.Run(t, f, in)
}

func TestContractOnNoop(t *testing.T) {
	telemetrytest.Run(t, telemetry.NoopFactory(), telemetrytest.Inputs{NoBackend: true, NoConfig: true})
}

func TestBrokenStubsFailExactlyTheirCases(t *testing.T) {
	tests := []struct {
		bug  bug
		want []string
	}{
		{bugBlocksRequests, []string{telemetrytest.CaseNeverBlocks}},
		{bugChangesResponse, []string{telemetrytest.CaseUsable, telemetrytest.CaseNeverBlocks, telemetrytest.CaseConcurrent}},
		{bugIgnoresShutdownContext, []string{telemetrytest.CaseShutdownReturns}},
		{bugDropsOnShutdown, []string{telemetrytest.CaseFlushesOnShutdown}},
		{bugAcceptsInvalid, []string{telemetrytest.CaseInvalidConfig}},
		{bugVagueError, []string{telemetrytest.CaseInvalidConfig}},
		{bugNotConcurrencySafe, []string{telemetrytest.CaseConcurrent}},
	}
	for _, tt := range tests {
		f, in := setup(tt.bug)
		report := telemetrytest.Check(t, f, in)
		if report.Aborted != "" {
			t.Errorf("bug %d: aborted: %s", tt.bug, report.Aborted)
			continue
		}
		if got := report.FailedCases(); !slices.Equal(got, tt.want) {
			t.Errorf("bug %d: failed cases %q, want %q", tt.bug, got, tt.want)
		}
		for _, c := range report.Cases {
			if !c.Passed && (c.Expected == "" || c.Observed == "") {
				t.Errorf("bug %d: case %q fails without expected and observed behaviour", tt.bug, c.Name)
			}
		}
	}
}

func TestAbortedRuns(t *testing.T) {
	f, in := setup(bugNone)

	noInputs := telemetrytest.Check(t, f, telemetrytest.Inputs{Config: in.Config})
	for _, name := range []string{"Inputs.StallBackend", "Inputs.Observed", "Inputs.InvalidConfig", "Inputs.FaultyValue",
		telemetrytest.CaseNeverBlocks, telemetrytest.CaseFlushesOnShutdown, telemetrytest.CaseInvalidConfig} {
		if !strings.Contains(noInputs.Aborted, name) {
			t.Errorf("missing inputs: %q does not name %q", noInputs.Aborted, name)
		}
	}
	if len(noInputs.Cases) != 0 || !noInputs.Failed() {
		t.Error("a run with missing inputs exercised cases or passed")
	}

	f.typ = " "
	if r := telemetrytest.Check(t, f, in); !strings.Contains(r.Aborted, "type name") {
		t.Errorf("empty type: aborted %q", r.Aborted)
	}

	nilFactory, in := setup(bugNilProvider)
	if r := telemetrytest.Check(t, nilFactory, in); !strings.Contains(r.Aborted, "cannot create") {
		t.Errorf("nil provider: aborted %q", r.Aborted)
	}

	broken, in := setup(bugNone)
	in.Config = map[string]any{"endpoint": invalidEndpoint}
	if r := telemetrytest.Check(t, broken, in); !strings.Contains(r.Aborted, invalidEndpoint) {
		t.Errorf("factory failure: aborted %q", r.Aborted)
	}
}
