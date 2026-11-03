package eventsinktest_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink/eventsinktest"
	"github.com/stretchr/testify/require"
)

// external is a stand-in for the external system a sink delivers to.
type external struct {
	mu      sync.Mutex
	gate    chan struct{} // closed while the system answers
	batches [][]events.Event
	refuse  int // 0 accepts, 1 refuses for now, 2 refuses for good
}

func newExternal() *external {
	gate := make(chan struct{})
	close(gate)
	return &external{gate: gate}
}

func (e *external) stall(testing.TB) func() {
	e.mu.Lock()
	gate := make(chan struct{})
	e.gate = gate
	e.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }
}

func (e *external) refuseWith(_ testing.TB, permanent bool) func() {
	e.mu.Lock()
	e.refuse = 1
	if permanent {
		e.refuse = 2
	}
	e.mu.Unlock()
	return func() {
		e.mu.Lock()
		e.refuse = 0
		e.mu.Unlock()
	}
}

func (e *external) received(testing.TB) [][]events.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.batches)
}

type bug int

const (
	bugNone bug = iota
	bugEmptyType
	bugNoKinds
	bugFailsOnValidConfig
	bugLosesEvents
	bugRetryableAsPermanent
	bugPermanentAsRetryable
	bugAcceptsInvalid
	bugVagueError
)

type stubFactory struct {
	ext *external
	bug bug
}

func (f stubFactory) Type() string {
	if f.bug == bugEmptyType {
		return ""
	}
	return "stub"
}

func (f stubFactory) Kinds() []events.Kind {
	if f.bug == bugNoKinds {
		return nil
	}
	return []events.Kind{events.KindEvaluation, events.KindTracking}
}

func (f stubFactory) Create(s eventsink.Settings) (eventsink.Sink, error) {
	endpoint, _ := s.Config["endpoint"].(string)
	switch {
	case f.bug == bugFailsOnValidConfig:
		return nil, errors.New("cannot connect")
	case endpoint == "bogus" && f.bug == bugVagueError:
		return nil, errors.New("invalid configuration")
	case endpoint == "bogus" && f.bug != bugAcceptsInvalid:
		return nil, errors.New(`invalid endpoint "bogus"`)
	}
	return stubSink{f.ext, f.bug}, nil
}

type stubSink struct {
	ext *external
	bug bug
}

func (s stubSink) Deliver(ctx context.Context, batch []events.Event) error {
	s.ext.mu.Lock()
	gate, refuse := s.ext.gate, s.ext.refuse
	s.ext.mu.Unlock()
	select {
	case <-gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	switch {
	case refuse == 1 && s.bug == bugRetryableAsPermanent:
		return eventsink.Permanent(errors.New("unavailable"))
	case refuse == 1:
		return errors.New("unavailable")
	case refuse == 2 && s.bug == bugPermanentAsRetryable:
		return errors.New("bad request")
	case refuse == 2:
		return eventsink.Permanent(errors.New("bad request"))
	}
	if s.bug == bugLosesEvents && len(batch) > 1 {
		batch = batch[:len(batch)-1]
	}
	s.ext.mu.Lock()
	defer s.ext.mu.Unlock()
	s.ext.batches = append(s.ext.batches, slices.Clone(batch))
	return nil
}

func inputs(ext *external) eventsinktest.Inputs {
	return eventsinktest.Inputs{
		Config:        map[string]any{"endpoint": "mem"},
		InvalidConfig: map[string]any{"endpoint": "bogus"},
		FaultyValue:   "bogus",
		Received:      ext.received,
		Stall:         ext.stall,
		Refuse:        ext.refuseWith,
		Timeout:       time.Second,
	}
}

func TestConformingSinkPassesTheContract(t *testing.T) {
	ext := newExternal()
	eventsinktest.Run(t, stubFactory{ext: ext}, inputs(ext))
}

func TestContractReportsBrokenSinks(t *testing.T) {
	tests := map[string]struct {
		bug    bug
		failed []string
	}{
		"loses events":              {bugLosesEvents, []string{eventsinktest.CaseKindsOnly, eventsinktest.CaseBatches, eventsinktest.CaseFlushesOnShutdown, eventsinktest.CaseRetryableDelivered}},
		"treats a failure as final": {bugRetryableAsPermanent, []string{eventsinktest.CaseRetryableDelivered}},
		"retries a refused batch":   {bugPermanentAsRetryable, []string{eventsinktest.CasePermanentNotRetried}},
		"accepts an invalid config": {bugAcceptsInvalid, []string{eventsinktest.CaseInvalidConfig}},
		"rejects without the value": {bugVagueError, []string{eventsinktest.CaseInvalidConfig}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ext := newExternal()
			report := eventsinktest.Check(t, stubFactory{ext: ext, bug: tt.bug}, inputs(ext))

			require.Empty(t, report.Aborted)
			require.True(t, report.Failed(), "the contract accepted a broken sink")
			require.ElementsMatch(t, tt.failed, report.FailedCases())
			for _, c := range report.Cases {
				if !c.Passed {
					require.NotEmpty(t, c.Expected)
					require.NotEmpty(t, c.Observed)
				}
			}
		})
	}
}

func TestContractAbortsWhereItCannotRun(t *testing.T) {
	ext := newExternal()

	for name, tt := range map[string]struct {
		factory eventsink.Factory
		mutate  func(*eventsinktest.Inputs)
		reason  string
	}{
		"empty type":       {stubFactory{ext: ext, bug: bugEmptyType}, nil, "type name is empty"},
		"no kinds":         {stubFactory{ext: ext, bug: bugNoKinds}, nil, "no event kind"},
		"factory fails":    {stubFactory{ext: ext, bug: bugFailsOnValidConfig}, nil, "cannot connect"},
		"no Received":      {stubFactory{ext: ext}, func(in *eventsinktest.Inputs) { in.Received = nil }, "Inputs.Received"},
		"no Stall":         {stubFactory{ext: ext}, func(in *eventsinktest.Inputs) { in.Stall = nil }, "Inputs.Stall"},
		"no Refuse":        {stubFactory{ext: ext}, func(in *eventsinktest.Inputs) { in.Refuse = nil }, "Inputs.Refuse"},
		"no InvalidConfig": {stubFactory{ext: ext}, func(in *eventsinktest.Inputs) { in.InvalidConfig = nil }, "Inputs.InvalidConfig"},
		"no FaultyValue":   {stubFactory{ext: ext}, func(in *eventsinktest.Inputs) { in.FaultyValue = "" }, "Inputs.FaultyValue"},
	} {
		t.Run(name, func(t *testing.T) {
			in := inputs(ext)
			if tt.mutate != nil {
				tt.mutate(&in)
			}
			report := eventsinktest.Check(t, tt.factory, in)
			require.Contains(t, report.Aborted, tt.reason)
			require.True(t, report.Failed())
			require.Empty(t, report.Cases)
		})
	}

	in := inputs(ext)
	in.NoConfig, in.InvalidConfig, in.FaultyValue = true, nil, ""
	report := eventsinktest.Check(t, stubFactory{ext: ext}, in)
	require.False(t, report.Failed(), "a sink without configuration needs no invalid one: %+v", report)
}

func TestDrops(t *testing.T) {
	d := eventsinktest.NewDrops()
	require.Zero(t, d.Total())
	d.Meter("x").Int64Counter("something.else").Add(context.Background(), 5)
	d.Meter("x").Int64Counter(eventsink.DroppedMetric).Add(context.Background(), 2)
	require.Equal(t, 2, d.Total(), "only the sink metric is counted")
	require.Equal(t, 2, d.Count(""), "without a reason attribute it counts under the empty reason")
}

// TestBrokenInputsFailTheirCases: the cases of the kinds, the buffer and the shutdown depend on what the author's inputs
// report, so an external system that does not behave as the contract assumes fails the cases that rely on it.
func TestBrokenInputsFailTheirCases(t *testing.T) {
	tests := map[string]struct {
		change func(ext *external, in *eventsinktest.Inputs)
		failed []string
	}{
		"receives an event of a kind the sink does not subscribe to": {
			change: func(ext *external, in *eventsinktest.Inputs) {
				received := in.Received
				in.Received = func(t testing.TB) [][]events.Event {
					var out [][]events.Event
					for _, batch := range received(t) {
						stray := events.ConfigurationChange{Time: time.Now(), Source: "stray", Format: "eventsinktest", Version: "1"}
						out = append(out, append(slices.Clone(batch), stray))
					}
					return out
				}
			},
			// the other cases count every event the system received, so the stray one fails them too
			failed: []string{eventsinktest.CaseKindsOnly, eventsinktest.CaseBatches, eventsinktest.CaseFlushesOnShutdown},
		},
		"does not stall when asked to": {
			change: func(ext *external, in *eventsinktest.Inputs) {
				in.Stall = func(testing.TB) func() { return func() {} }
			},
			failed: []string{eventsinktest.CaseFullBufferDrops, eventsinktest.CaseReturnsInTime},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ext := newExternal()
			in := inputs(ext)
			tt.change(ext, &in)
			report := eventsinktest.Check(t, stubFactory{ext: ext}, in)

			require.Empty(t, report.Aborted)
			require.ElementsMatch(t, tt.failed, report.FailedCases())
			for _, c := range report.Cases {
				if !c.Passed {
					require.NotEmpty(t, c.Expected)
					require.NotEmpty(t, c.Observed)
				}
			}
		})
	}
}
