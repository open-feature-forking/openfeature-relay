// Package telemetrytest holds the contract tests of the telemetry provider component type (UC-008). An Extension
// Author hands the runner the provider's factory and the test inputs:
//
//	func TestContract(t *testing.T) {
//		telemetrytest.Run(t, myFactory{}, telemetrytest.Inputs{Config: ..., StallBackend: ..., Observed: ...})
//	}
//
// The runner creates the provider only through its factory, once per case. Each contract case is a subtest. A run
// that cannot exercise the contract (a missing test input, an invalid declaration, a factory that fails on the valid
// configuration) fails as a whole and names why; no case can be skipped.
package telemetrytest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// RequestLimit is how long a request through the provider's HTTP wrappers may take while the backend stalls.
const RequestLimit = 100 * time.Millisecond

// ShutdownLimit is the context a case gives Shutdown while the backend stalls.
const ShutdownLimit = 200 * time.Millisecond

// DefaultTimeout is the time limit of a case that has none of its own.
const DefaultTimeout = 5 * time.Second

// Goroutines is the number of goroutines of the concurrency case.
const Goroutines = 64

// Names the contract records in the flush case; Observed must report them after Shutdown.
const (
	FlushSpan   = "telemetrytest.flush"
	FlushMetric = "telemetrytest.flush.count"
)

// Names of the contract cases of a telemetry provider (UC-008 BR-002).
const (
	CaseUsable            = "is usable without further setup"
	CaseNeverBlocks       = "never blocks a request while its backend stalls"
	CaseConcurrent        = "works under concurrent use"
	CaseShutdownReturns   = "returns from shutdown within its context while its backend stalls"
	CaseFlushesOnShutdown = "delivers held data on shutdown"
	CaseInvalidConfig     = "rejects an invalid configuration naming the faulty value"
)

// Observation is what the backend received: the names of the spans and of the metrics.
type Observation struct {
	Spans   []string
	Metrics []string
}

// Inputs are the test inputs a telemetry provider's contract needs.
type Inputs struct {
	// Config is a valid configuration.
	Config map[string]any
	// InvalidConfig is a configuration the factory must reject with an error that contains FaultyValue.
	InvalidConfig map[string]any
	FaultyValue   string
	// NoConfig declares that the provider takes no configuration; InvalidConfig and FaultyValue are then not needed.
	NoConfig bool
	// StallBackend makes the backend of a created provider stop accepting data until release is called.
	StallBackend func(t testing.TB) (release func())
	// Observed reports what the backend received.
	Observed func(t testing.TB) Observation
	// NoBackend declares that the provider delivers nothing anywhere, as the noop provider does; StallBackend and
	// Observed are then not needed.
	NoBackend bool
	// Timeout is the time limit of a case; zero means DefaultTimeout.
	Timeout time.Duration
}

// Case is the outcome of one contract case.
type Case struct {
	Name     string
	Passed   bool
	Expected string
	Observed string
}

// Report is the outcome of a run.
type Report struct {
	// Aborted says why no contract case was exercised.
	Aborted string
	Cases   []Case
}

// Failed reports whether the run is a failure: it was aborted or a case failed.
func (r Report) Failed() bool {
	return r.Aborted != "" || len(r.FailedCases()) > 0
}

// FailedCases names the cases that failed.
func (r Report) FailedCases() []string {
	var names []string
	for _, c := range r.Cases {
		if !c.Passed {
			names = append(names, c.Name)
		}
	}
	return names
}

func (r Report) report(t *testing.T) {
	t.Helper()
	if r.Aborted != "" {
		t.Fatalf("the contract tests were not run: %s", r.Aborted)
	}
	for _, c := range r.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if !c.Passed {
				t.Errorf("expected: %s\nobserved: %s", c.Expected, c.Observed)
			}
		})
	}
}

// Run runs the contract tests of a telemetry provider: one subtest per contract case.
func Run(t *testing.T, f telemetry.Factory, in Inputs) {
	t.Helper()
	Check(t, f, in).report(t)
}

// Check runs the contract tests of a telemetry provider and returns their outcome. t is only handed to the test
// inputs.
func Check(t testing.TB, f telemetry.Factory, in Inputs) Report {
	if msg := missing(in); msg != "" {
		return Report{Aborted: msg}
	}
	if err := telemetry.ValidateDeclarations(f); err != nil {
		return Report{Aborted: fmt.Sprintf("invalid declaration: %v", err)}
	}
	r := runner{t: t, f: f, in: in, limit: in.Timeout}
	if r.limit <= 0 {
		r.limit = DefaultTimeout
	}
	p, err := r.create(in.Config)
	if err != nil || p == nil {
		return Report{Aborted: fmt.Sprintf("the factory cannot create the provider from the valid configuration (provider %v, error %v)", p, err)}
	}
	_ = shutdown(p, r.limit)

	cases := []struct {
		name string
		run  func() (expected, observed string)
	}{
		{CaseUsable, r.usable},
		{CaseNeverBlocks, r.neverBlocks},
		{CaseConcurrent, r.concurrent},
		{CaseShutdownReturns, r.shutdownReturns},
		{CaseFlushesOnShutdown, r.flushesOnShutdown},
		{CaseInvalidConfig, r.invalidConfig},
	}
	report := Report{}
	for _, c := range cases {
		expected, observed := guarded(c.run)
		report.Cases = append(report.Cases, Case{Name: c.name, Passed: observed == "", Expected: expected, Observed: observed})
	}
	return report
}

func missing(in Inputs) string {
	var parts []string
	need := func(name string, set bool, cases ...string) {
		if !set {
			quoted := make([]string, len(cases))
			for i, c := range cases {
				quoted[i] = fmt.Sprintf("%q", c)
			}
			parts = append(parts, fmt.Sprintf("test input %s is missing, needed by the cases %s", name, strings.Join(quoted, ", ")))
		}
	}
	need("Inputs.StallBackend", in.NoBackend || in.StallBackend != nil, CaseNeverBlocks, CaseShutdownReturns)
	need("Inputs.Observed", in.NoBackend || in.Observed != nil, CaseFlushesOnShutdown)
	need("Inputs.InvalidConfig", in.NoConfig || in.InvalidConfig != nil, CaseInvalidConfig)
	need("Inputs.FaultyValue", in.NoConfig || in.FaultyValue != "", CaseInvalidConfig)
	return strings.Join(parts, "; ")
}

// guarded runs a case and turns a panic into its observed behaviour. A case reports "" as observed when it passed.
func guarded(run func() (string, string)) (expected, observed string) {
	defer func() {
		if v := recover(); v != nil {
			observed = fmt.Sprintf("panic: %v", v)
		}
	}()
	return run()
}

type runner struct {
	t     testing.TB
	f     telemetry.Factory
	in    Inputs
	limit time.Duration
}

func (r runner) create(config map[string]any) (telemetry.Provider, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.limit)
	defer cancel()
	return r.f.Create(ctx, telemetry.Settings{
		Logger:              logger.NewLogger(nil, false),
		DistributionName:    "telemetrytest",
		DistributionVersion: "0.0.0",
		Config:              config,
	})
}

func (r runner) mustCreate() (telemetry.Provider, string) {
	p, err := r.create(r.in.Config)
	if err != nil || p == nil {
		return nil, fmt.Sprintf("the factory failed on the valid configuration (provider %v, error %v)", p, err)
	}
	return p, ""
}

// record uses every part of the provider once.
func record(ctx context.Context, p telemetry.Provider, i int) {
	ctx, span := p.Tracer("telemetrytest").Start(ctx, "telemetrytest.span",
		telemetry.WithSpanKind(telemetry.SpanKindServer), telemetry.WithAttributes(telemetry.Int("i", i)))
	span.SetAttributes(telemetry.String("k", "v"), telemetry.Bool("b", true), telemetry.Float64("f", 0.5))
	if i%2 == 1 {
		span.RecordError(fmt.Errorf("error %d", i))
		span.SetError("failed")
	}
	m := p.Meter("telemetrytest")
	m.Int64Counter("telemetrytest.count", telemetry.WithUnit("{call}")).Add(ctx, 1, telemetry.String("k", "v"))
	m.Int64UpDownCounter("telemetrytest.active").Add(ctx, -1)
	m.Float64Histogram("telemetrytest.duration", telemetry.WithUnit("s"), telemetry.WithBuckets(0.1, 1, 10)).
		Record(ctx, 0.5, telemetry.Int64("n", int64(i)))
	span.End()
}

const (
	handlerStatus = http.StatusCreated
	handlerBody   = "telemetrytest"
	handlerHeader = "X-Telemetrytest"
	requestPath   = "/telemetrytest/route"
)

// handler answers with a fixed response and fails the request when the wrappers changed it.
var handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != requestPath || req.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.Header().Set(handlerHeader, "1")
	w.WriteHeader(handlerStatus)
	_, _ = io.WriteString(w, handlerBody)
})

func wrap(p telemetry.Provider) http.Handler {
	return p.HTTPServerMetrics(p.HTTPServerSpans(handler, "telemetrytest"), telemetry.HTTPMetricsOptions{
		Service: "telemetrytest",
		Route:   "/telemetrytest/{route}",
	})
}

// roundTrip sends one request through h and describes how the response differs from the handler's, "" if not.
func roundTrip(h http.Handler) string {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, requestPath, strings.NewReader("{}")))
	if rec.Code != handlerStatus || rec.Body.String() != handlerBody || rec.Header().Get(handlerHeader) != "1" {
		return fmt.Sprintf("the wrapped handler answered %d %q (header %q)", rec.Code, rec.Body.String(), rec.Header().Get(handlerHeader))
	}
	return ""
}

// within runs f and reports whether it returned within d.
func within(d time.Duration, f func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func shutdown(p telemetry.Provider, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	return p.Shutdown(ctx)
}

func (r runner) stall() func() {
	if r.in.NoBackend {
		return func() {}
	}
	return r.in.StallBackend(r.t)
}

func (r runner) usable() (string, string) {
	const expected = "spans, instruments and both HTTP wrappers work right after Create; the wrapped handler's response is unchanged"
	p, failure := r.mustCreate()
	if failure != "" {
		return expected, failure
	}
	defer func() { _ = shutdown(p, r.limit) }()
	ctx, span := p.Tracer("telemetrytest").Start(context.Background(), "telemetrytest.usable")
	if ctx == nil || span == nil {
		return expected, fmt.Sprintf("Start returned context %v and span %v", ctx, span)
	}
	span.End()
	record(context.Background(), p, 0)
	if diff := roundTrip(wrap(p)); diff != "" {
		return expected, diff
	}
	return expected, ""
}

func (r runner) neverBlocks() (string, string) {
	expected := fmt.Sprintf("with a stalled backend, a request through both HTTP wrappers and recording a span and metrics return within %v", RequestLimit)
	p, failure := r.mustCreate()
	if failure != "" {
		return expected, failure
	}
	release := r.stall()
	defer func() {
		release()
		_ = shutdown(p, r.limit)
	}()
	h := wrap(p)
	var diff string
	// warm up once, so that a provider that exports on its first use meets the stalled backend
	if !within(r.limit, func() { record(context.Background(), p, 0) }) {
		return expected, fmt.Sprintf("recording blocked for more than %v", r.limit)
	}
	if !within(RequestLimit, func() {
		for i := range 10 {
			record(context.Background(), p, i)
		}
		diff = roundTrip(h)
	}) {
		return expected, fmt.Sprintf("still blocked after %v", RequestLimit)
	}
	if diff != "" {
		return expected, diff
	}
	return expected, ""
}

func (r runner) concurrent() (string, string) {
	expected := fmt.Sprintf("%d goroutines record spans and metrics and send requests at once without a panic or a changed response", Goroutines)
	p, failure := r.mustCreate()
	if failure != "" {
		return expected, failure
	}
	defer func() { _ = shutdown(p, r.limit) }()
	h := wrap(p)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		problems []string
	)
	for g := range Goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if v := recover(); v != nil {
					mu.Lock()
					problems = append(problems, fmt.Sprintf("goroutine %d panicked: %v", g, v))
					mu.Unlock()
				}
			}()
			for i := range 20 {
				record(context.Background(), p, g*20+i)
				if diff := roundTrip(h); diff != "" {
					mu.Lock()
					problems = append(problems, diff)
					mu.Unlock()
					return
				}
			}
		}()
	}
	if !within(r.limit, wg.Wait) {
		return expected, fmt.Sprintf("the goroutines did not finish within %v", r.limit)
	}
	if len(problems) > 0 {
		return expected, problems[0]
	}
	return expected, ""
}

func (r runner) shutdownReturns() (string, string) {
	expected := fmt.Sprintf("with a stalled backend, Shutdown returns within its context of %v", ShutdownLimit)
	p, failure := r.mustCreate()
	if failure != "" {
		return expected, failure
	}
	record(context.Background(), p, 0)
	release := r.stall()
	defer release()
	// a little slack for the scheduler
	if !within(ShutdownLimit+100*time.Millisecond, func() { _ = shutdown(p, ShutdownLimit) }) {
		return expected, fmt.Sprintf("Shutdown had not returned after %v", ShutdownLimit+100*time.Millisecond)
	}
	return expected, ""
}

func (r runner) flushesOnShutdown() (string, string) {
	if r.in.NoBackend {
		const expected = "a provider without a backend returns nil from Shutdown"
		p, failure := r.mustCreate()
		if failure != "" {
			return expected, failure
		}
		if err := shutdown(p, r.limit); err != nil {
			return expected, fmt.Sprintf("Shutdown returned %v", err)
		}
		return expected, ""
	}
	expected := fmt.Sprintf("after Shutdown the backend has received the span %q and the metric %q recorded just before it", FlushSpan, FlushMetric)
	p, failure := r.mustCreate()
	if failure != "" {
		return expected, failure
	}
	ctx, span := p.Tracer("telemetrytest").Start(context.Background(), FlushSpan)
	p.Meter("telemetrytest").Int64Counter(FlushMetric).Add(ctx, 1)
	span.End()
	if err := shutdown(p, r.limit); err != nil {
		return expected, fmt.Sprintf("Shutdown returned %v", err)
	}
	seen := r.in.Observed(r.t)
	if !slices.Contains(seen.Spans, FlushSpan) || !slices.Contains(seen.Metrics, FlushMetric) {
		return expected, fmt.Sprintf("the backend received the spans %v and the metrics %v", seen.Spans, seen.Metrics)
	}
	return expected, ""
}

func (r runner) invalidConfig() (string, string) {
	if r.in.NoConfig {
		const expected = "a provider that takes no configuration is created from any configuration"
		p, err := r.create(map[string]any{"telemetrytest": "unknown"})
		if err != nil || p == nil {
			return expected, fmt.Sprintf("Create returned provider %v and error %v", p, err)
		}
		_ = shutdown(p, r.limit)
		return expected, ""
	}
	expected := fmt.Sprintf("Create rejects the invalid configuration with an error that names %q", r.in.FaultyValue)
	p, err := r.create(r.in.InvalidConfig)
	if err == nil {
		if p != nil {
			_ = shutdown(p, r.limit)
		}
		return expected, "Create accepted it"
	}
	if !strings.Contains(err.Error(), r.in.FaultyValue) {
		return expected, fmt.Sprintf("the error %q does not name it", err)
	}
	return expected, ""
}
