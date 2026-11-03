// Package eventsinktest holds the contract tests of the event sink component type (UC-008). An Extension Author
// hands the runner the sink's factory and the test inputs:
//
//	func TestContract(t *testing.T) {
//		eventsinktest.Run(t, myFactory{}, eventsinktest.Inputs{Config: ..., Received: ..., Stall: ..., Refuse: ...})
//	}
//
// The runner creates the sink only through its factory, inside the core's pipeline, once per case. Each contract
// case is a subtest. A run that cannot exercise the contract (a missing test input, an invalid declaration, a
// factory that fails on the valid configuration) fails as a whole and names why; no case can be skipped.
package eventsinktest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// PublishLimit is how long publishing events may take while the external system stalls.
const PublishLimit = 200 * time.Millisecond

// ShutdownLimit is the context a case gives Close while the external system stalls.
const ShutdownLimit = 200 * time.Millisecond

// SettleTime is how long the contract waits between cases for requests of the previous one to end.
const SettleTime = 50 * time.Millisecond

// DefaultTimeout is the time limit of a case that has none of its own.
const DefaultTimeout = 5 * time.Second

// Names of the contract cases of an event sink (UC-008 BR-002).
const (
	CaseKindsOnly           = "receives only the kinds it subscribes to"
	CaseBatches             = "receives events in batches"
	CaseFullBufferDrops     = "drops and counts events instead of blocking the publisher when the buffer is full"
	CaseFlushesOnShutdown   = "delivers the buffered events on shutdown"
	CaseReturnsInTime       = "returns from shutdown within its context while the external system stalls"
	CaseRetryableDelivered  = "delivers a batch the external system refused for now once it accepts again"
	CasePermanentNotRetried = "does not repeat a batch the external system refused for good"
	CaseInvalidConfig       = "rejects an invalid configuration naming the faulty value"
)

// Inputs are the test inputs an event sink's contract needs.
type Inputs struct {
	// Config is a valid configuration. The contract adds the core's own keys (bufferSize, batchSize, flushInterval,
	// maxAttempts) where a case needs them.
	Config map[string]any
	// InvalidConfig is a configuration the factory must reject with an error that contains FaultyValue.
	InvalidConfig map[string]any
	FaultyValue   string
	// NoConfig declares that the sink takes no configuration; InvalidConfig and FaultyValue are then not needed.
	NoConfig bool
	// Received reports the batches the external system received, in order. The contract tells the batches of a case from those of earlier ones by their number.
	Received func(t testing.TB) [][]events.Event
	// Stall makes the external system stop answering until release is called.
	Stall func(t testing.TB) (release func())
	// Refuse makes the external system refuse every batch until restore is called: for now where permanent is
	// false (such as a 503), for good where it is true (such as a 400). The sink reports the first as a failure
	// and the second wrapped by eventsink.Permanent.
	Refuse func(t testing.TB, permanent bool) (restore func())
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

// Run runs the contract tests of an event sink: one subtest per contract case.
func Run(t *testing.T, f eventsink.Factory, in Inputs) {
	t.Helper()
	Check(t, f, in).report(t)
}

// Check runs the contract tests of an event sink and returns their outcome. t is only handed to the test inputs.
func Check(t testing.TB, f eventsink.Factory, in Inputs) Report {
	if msg := missing(in); msg != "" {
		return Report{Aborted: msg}
	}
	if err := eventsink.ValidateDeclarations(f); err != nil {
		return Report{Aborted: fmt.Sprintf("invalid declaration: %v", err)}
	}
	r := runner{t: t, f: f, in: in, limit: in.Timeout}
	if r.limit <= 0 {
		r.limit = DefaultTimeout
	}
	if _, err := f.Create(eventsink.Settings{Logger: logger.NewLogger(nil, false), Config: in.Config}); err != nil {
		return Report{Aborted: fmt.Sprintf("the factory cannot create the sink from the valid configuration (error %v)", err)}
	}

	cases := []struct {
		name string
		run  func() (expected, observed string)
	}{
		{CaseKindsOnly, r.kindsOnly},
		{CaseBatches, r.batches},
		{CaseFullBufferDrops, r.fullBufferDrops},
		{CaseFlushesOnShutdown, r.flushesOnShutdown},
		{CaseReturnsInTime, r.returnsInTime},
		{CaseRetryableDelivered, r.retryableDelivered},
		{CasePermanentNotRetried, r.permanentNotRetried},
		{CaseInvalidConfig, r.invalidConfig},
	}
	report := Report{}
	for _, c := range cases {
		expected, observed := guarded(c.run)
		report.Cases = append(report.Cases, Case{Name: c.name, Passed: observed == "", Expected: expected, Observed: observed})
		// a request an earlier case cancelled may still reach the external system; it must not count for the next case
		time.Sleep(SettleTime)
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
	need("Inputs.Received", in.Received != nil, CaseKindsOnly, CaseBatches, CaseFlushesOnShutdown, CaseRetryableDelivered, CasePermanentNotRetried)
	need("Inputs.Stall", in.Stall != nil, CaseFullBufferDrops, CaseReturnsInTime)
	need("Inputs.Refuse", in.Refuse != nil, CaseRetryableDelivered, CasePermanentNotRetried)
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
	f     eventsink.Factory
	in    Inputs
	limit time.Duration
}

// Drops is a telemetry provider that records how many events the pipeline dropped.
type Drops struct {
	telemetry.Provider
	mu     sync.Mutex
	counts map[string]int
}

// NewDrops returns a provider that counts the events the event sink metric reports as dropped.
func NewDrops() *Drops { return &Drops{Provider: telemetry.Noop(), counts: map[string]int{}} }

// Meter implements telemetry.Provider.
func (d *Drops) Meter(scope string) telemetry.Meter {
	return dropMeter{Meter: d.Provider.Meter(scope), drops: d}
}

// Count is the number of events dropped for the reason, of every kind.
func (d *Drops) Count(reason string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.counts[reason]
}

// Total is the number of events dropped for every reason.
func (d *Drops) Total() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	total := 0
	for _, n := range d.counts {
		total += n
	}
	return total
}

type dropMeter struct {
	telemetry.Meter
	drops *Drops
}

func (m dropMeter) Int64Counter(name string, opts ...telemetry.InstrumentOption) telemetry.Int64Counter {
	if name != eventsink.DroppedMetric {
		return m.Meter.Int64Counter(name, opts...)
	}
	return dropCounter{m.drops}
}

type dropCounter struct{ drops *Drops }

func (c dropCounter) Add(_ context.Context, n int64, attrs ...telemetry.Attribute) {
	reason := ""
	for _, a := range attrs {
		if a.Key == "reason" {
			reason = a.Value.AsString()
		}
	}
	c.drops.mu.Lock()
	defer c.drops.mu.Unlock()
	c.drops.counts[reason] += int(n)
}

// config returns the valid configuration with the core's keys set.
func (r runner) config(core map[string]any) map[string]any {
	c := map[string]any{}
	for k, v := range r.in.Config {
		c[k] = v
	}
	for k, v := range core {
		c[k] = v
	}
	return c
}

func (r runner) start(core map[string]any) (*eventsink.Pipeline, *events.Bus, *Drops, string) {
	bus := events.NewBus()
	drops := NewDrops()
	p, err := eventsink.NewPipeline(bus, r.f, eventsink.Settings{
		Logger: logger.NewLogger(nil, false), Telemetry: drops, Config: r.config(core),
	}, eventsink.WithBackoff(func(int) time.Duration { return 5 * time.Millisecond }))
	if err != nil {
		return nil, nil, nil, fmt.Sprintf("the pipeline cannot start with the valid configuration: %v", err)
	}
	return p, bus, drops, ""
}

// event returns the i-th event of the kind, identifiable by identity.
func event(kind events.Kind, i int) events.Event {
	name := fmt.Sprintf("eventsinktest-%d", i)
	switch kind {
	case events.KindConfigurationChange:
		return events.ConfigurationChange{Time: time.Now(), Source: name, Format: "eventsinktest", Version: "1"}
	case events.KindTracking:
		return events.Tracking{Time: time.Now(), EventName: name, Origin: events.OriginService}
	}
	return events.Evaluation{Time: time.Now(), FlagKey: name, Reason: "STATIC", Origin: events.OriginService}
}

func flatten(batches [][]events.Event) []events.Event {
	var all []events.Event
	for _, b := range batches {
		all = append(all, b...)
	}
	return all
}

// mark returns the number of batches received so far, to tell a case's own batches from those of earlier cases.
func (r runner) mark() int { return len(r.in.Received(r.t)) }

// since returns the batches received after mark.
func (r runner) since(mark int) [][]events.Event {
	all := r.in.Received(r.t)
	if mark > len(all) {
		return nil
	}
	return all[mark:]
}

func (r runner) kind() events.Kind { return r.f.Kinds()[0] }

func (r runner) close(p *eventsink.Pipeline, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	return p.Close(ctx)
}

// eventually polls cond until it holds or the case's time limit passes.
func (r runner) eventually(cond func() bool) bool {
	deadline := time.Now().Add(r.limit)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
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

func (r runner) kindsOnly() (string, string) {
	const expected = "events of the kinds the factory declares arrive, events of the other kinds do not"
	mark := r.mark()
	p, bus, _, failure := r.start(nil)
	if failure != "" {
		return expected, failure
	}
	for i, k := range []events.Kind{events.KindConfigurationChange, events.KindEvaluation, events.KindTracking} {
		bus.Publish(event(k, i))
	}
	if err := r.close(p, r.limit); err != nil {
		return expected, fmt.Sprintf("Close returned %v", err)
	}
	declared := map[events.Kind]bool{}
	for _, k := range r.f.Kinds() {
		declared[k] = true
	}
	got := map[events.Kind]bool{}
	for _, e := range flatten(r.since(mark)) {
		got[e.EventKind()] = true
		if !declared[e.EventKind()] {
			return expected, fmt.Sprintf("the sink received an event of the kind %q it does not subscribe to", e.EventKind())
		}
	}
	for k := range declared {
		if !got[k] {
			return expected, fmt.Sprintf("the sink received no event of the kind %q it subscribes to", k)
		}
	}
	return expected, ""
}

func (r runner) batches() (string, string) {
	const total, batchSize = 25, 10
	expected := fmt.Sprintf("%d events published with batchSize %d arrive complete, in batches of at most %d, in fewer than %d batches", total, batchSize, batchSize, total)
	mark := r.mark()
	p, bus, _, failure := r.start(map[string]any{eventsink.KeyBatchSize: batchSize, eventsink.KeyFlushInterval: time.Hour})
	if failure != "" {
		return expected, failure
	}
	for i := 0; i < total; i++ {
		bus.Publish(event(r.kind(), i))
	}
	if err := r.close(p, r.limit); err != nil {
		return expected, fmt.Sprintf("Close returned %v", err)
	}
	batches := r.since(mark)
	for _, b := range batches {
		if len(b) > batchSize {
			return expected, fmt.Sprintf("a batch of %d events", len(b))
		}
	}
	if n := len(flatten(batches)); n != total {
		return expected, fmt.Sprintf("%d events in %d batches", n, len(batches))
	}
	if len(batches) >= total {
		return expected, fmt.Sprintf("%d batches for %d events: no batching", len(batches), total)
	}
	return expected, ""
}

func (r runner) fullBufferDrops() (string, string) {
	const published, buffer = 200, 5
	expected := fmt.Sprintf("with a stalled external system and a buffer of %d, publishing %d events returns within %v and counts all but a handful as dropped (buffer_full)", buffer, published, PublishLimit)
	p, bus, drops, failure := r.start(map[string]any{eventsink.KeyBufferSize: buffer, eventsink.KeyBatchSize: 1, eventsink.KeyMaxAttempts: 1000})
	if failure != "" {
		return expected, failure
	}
	release := r.in.Stall(r.t)
	defer release()
	defer func() { _ = r.close(p, ShutdownLimit) }()

	if !within(PublishLimit, func() {
		for i := 0; i < published; i++ {
			bus.Publish(event(r.kind(), i))
		}
	}) {
		return expected, fmt.Sprintf("publishing did not return within %v", PublishLimit)
	}
	// at most the buffer, the batch being delivered and one event in between can be accepted
	if n := drops.Count(eventsink.ReasonBufferFull); n < published-buffer-3 {
		return expected, fmt.Sprintf("%d events counted as dropped (buffer_full)", n)
	}
	return expected, ""
}

func (r runner) flushesOnShutdown() (string, string) {
	const total = 10
	expected := fmt.Sprintf("%d buffered events, none flushed yet by size or age, are delivered on Close, and none is counted as dropped", total)
	mark := r.mark()
	p, bus, drops, failure := r.start(map[string]any{eventsink.KeyBatchSize: 1000, eventsink.KeyFlushInterval: time.Hour})
	if failure != "" {
		return expected, failure
	}
	for i := 0; i < total; i++ {
		bus.Publish(event(r.kind(), i))
	}
	if err := r.close(p, r.limit); err != nil {
		return expected, fmt.Sprintf("Close returned %v", err)
	}
	if n := len(flatten(r.since(mark))); n != total || drops.Total() != 0 {
		return expected, fmt.Sprintf("%d events received, %d counted as dropped", n, drops.Total())
	}
	return expected, ""
}

func (r runner) returnsInTime() (string, string) {
	const total = 10
	expected := fmt.Sprintf("Close returns within its context of %v while the external system stalls, and counts the %d undelivered events as dropped (shutdown)", ShutdownLimit, total)
	p, bus, drops, failure := r.start(map[string]any{eventsink.KeyBatchSize: 1, eventsink.KeyMaxAttempts: 1000})
	if failure != "" {
		return expected, failure
	}
	release := r.in.Stall(r.t)
	defer release()
	for i := 0; i < total; i++ {
		bus.Publish(event(r.kind(), i))
	}
	time.Sleep(20 * time.Millisecond)
	var err error
	if !within(ShutdownLimit+PublishLimit, func() { err = r.close(p, ShutdownLimit) }) {
		return expected, fmt.Sprintf("Close did not return within %v", ShutdownLimit+PublishLimit)
	}
	if err == nil {
		return expected, "Close reported success although nothing was delivered"
	}
	if n := drops.Count(eventsink.ReasonShutdown); n != total {
		return expected, fmt.Sprintf("%d events counted as dropped (shutdown)", n)
	}
	return expected, ""
}

func (r runner) retryableDelivered() (string, string) {
	const total = 5
	expected := fmt.Sprintf("%d events refused for now are delivered after the external system accepts again, and none is dropped", total)
	mark := r.mark()
	p, bus, drops, failure := r.start(map[string]any{eventsink.KeyBatchSize: total, eventsink.KeyMaxAttempts: 100000})
	if failure != "" {
		return expected, failure
	}
	restore := r.in.Refuse(r.t, false)
	defer restore()
	defer func() { _ = r.close(p, ShutdownLimit) }()
	for i := 0; i < total; i++ {
		bus.Publish(event(r.kind(), i))
	}
	time.Sleep(100 * time.Millisecond)
	restore()
	if !r.eventually(func() bool { return len(flatten(r.since(mark))) >= total }) {
		return expected, fmt.Sprintf("%d events received, %d counted as dropped", len(flatten(r.since(mark))), drops.Total())
	}
	if drops.Total() != 0 {
		return expected, fmt.Sprintf("%d events counted as dropped", drops.Total())
	}
	return expected, ""
}

func (r runner) permanentNotRetried() (string, string) {
	const total = 5
	expected := fmt.Sprintf("%d events refused for good are counted as dropped (permanent) and never delivered, even after the external system accepts again", total)
	mark := r.mark()
	p, bus, drops, failure := r.start(map[string]any{eventsink.KeyBatchSize: 1, eventsink.KeyMaxAttempts: 100000})
	if failure != "" {
		return expected, failure
	}
	restore := r.in.Refuse(r.t, true)
	defer restore()
	defer func() { _ = r.close(p, ShutdownLimit) }()
	for i := 0; i < total; i++ {
		bus.Publish(event(r.kind(), i))
	}
	if !r.eventually(func() bool { return drops.Count(eventsink.ReasonPermanent) >= total }) {
		return expected, fmt.Sprintf("%d events counted as dropped (permanent)", drops.Count(eventsink.ReasonPermanent))
	}
	restore()
	time.Sleep(150 * time.Millisecond)
	if n := len(flatten(r.since(mark))); n != 0 {
		return expected, fmt.Sprintf("%d refused events were delivered later", n)
	}
	return expected, ""
}

func (r runner) invalidConfig() (string, string) {
	if r.in.NoConfig {
		return "", ""
	}
	expected := fmt.Sprintf("the factory rejects the configuration with an error that contains %q", r.in.FaultyValue)
	_, err := r.f.Create(eventsink.Settings{Logger: logger.NewLogger(nil, false), Config: r.in.InvalidConfig})
	if err == nil {
		return expected, "the factory accepted the configuration"
	}
	if !strings.Contains(err.Error(), r.in.FaultyValue) {
		return expected, fmt.Sprintf("the error is %q", err.Error())
	}
	return expected, ""
}
