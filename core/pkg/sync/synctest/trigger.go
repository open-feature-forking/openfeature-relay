package synctest

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
)

// TriggerInputs are the test inputs a fetch trigger's contract needs.
type TriggerInputs struct {
	// Source and Config create the trigger through its factory, as a distribution does for a source's `triggers`
	// entry.
	Source sync.SourceConfig
	Config map[string]any
	// Signal makes the trigger's change signal arrive once.
	Signal Input
	// Interrupt makes the trigger lose its connection to the change signal.
	Interrupt Input
	// Timeout is the time limit of a case; zero means DefaultTimeout.
	Timeout time.Duration
}

// Names of the contract cases of a fetch trigger (UC-008 BR-002). That a trigger never fetches or delivers itself
// holds by construction: Run is handed the signal function and nothing else.
const (
	CaseSignalsEachTime  = "signals each time the change signal arrives"
	CaseReconnects       = "reconnects after losing the change signal"
	CaseStopsListening   = "stops listening on shutdown"
	CaseReturnsNilOnDone = "returns nil from Run on shutdown"
)

// RunTrigger runs the contract tests of a fetch trigger: one subtest per contract case.
func RunTrigger(t *testing.T, f sync.TriggerFactory, in TriggerInputs) {
	t.Helper()
	CheckTrigger(t, f, in).report(t)
}

// CheckTrigger runs the contract tests of a fetch trigger and returns their outcome. t is only handed to the test
// inputs.
func CheckTrigger(t testing.TB, f sync.TriggerFactory, in TriggerInputs) Report {
	if msg := missing(
		missingInput{"TriggerInputs.Signal", in.Signal != nil, []string{CaseSignalsEachTime, CaseReconnects, CaseStopsListening}},
		missingInput{"TriggerInputs.Interrupt", in.Interrupt != nil, []string{CaseReconnects}},
	); msg != "" {
		return Report{Aborted: msg}
	}
	if err := sync.ValidateDeclarations(f); err != nil {
		return Report{Aborted: fmt.Sprintf("invalid declarations: %v", err)}
	}
	trigger, err := f.CreateTrigger(newSettings(in.Source, f.Type(), in.Config))
	if err != nil || trigger == nil {
		return Report{Aborted: fmt.Sprintf("the factory cannot create the fetch trigger (trigger %v, error %v)", trigger, err)}
	}

	limit := timeoutOr(in.Timeout)
	signals := make(chan struct{}, 64)
	signal := func() {
		select {
		case signals <- struct{}{}:
		default:
		}
	}

	// run the trigger the way the fetching sync provider does: again after an error, until the context is done
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runs, lastReturnedNil atomic.Int32
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for ctx.Err() == nil {
			runs.Add(1)
			err := trigger.Run(ctx, signal)
			if ctx.Err() != nil {
				if err == nil {
					lastReturnedNil.Store(1)
				}
				return
			}
			if err == nil {
				return // returned without being asked to: it will not signal again
			}
			time.Sleep(time.Millisecond)
		}
	}()

	awaitSignal := func(d time.Duration) bool {
		select {
		case <-signals:
			return true
		case <-time.After(d):
			return false
		}
	}
	drain := func() {
		for {
			select {
			case <-signals:
			default:
				return
			}
		}
	}

	var cases []Case

	in.Signal(t)
	one := awaitSignal(limit)
	in.Signal(t)
	two := awaitSignal(limit)
	cases = append(cases, check(CaseSignalsEachTime, one && two,
		"one signal for each arrival of the change signal, here two",
		fmt.Sprintf("first arrival signalled: %v, second arrival signalled: %v", one, two)))

	drain()
	in.Interrupt(t)
	in.Signal(t)
	after := awaitSignal(limit)
	cases = append(cases, check(CaseReconnects, after,
		"a signal for a change signal that arrives after the connection was lost",
		fmt.Sprintf("signalled: %v", after)))

	cancel()
	stopped := false
	select {
	case <-finished:
		stopped = true
	case <-time.After(limit):
	}
	cases = append(cases, check(CaseReturnsNilOnDone, stopped && lastReturnedNil.Load() == 1,
		fmt.Sprintf("Run returns nil within %s of the context being done", limit),
		fmt.Sprintf("returned: %v, returned nil: %v", stopped, lastReturnedNil.Load() == 1)))

	drain()
	in.Signal(t)
	late := awaitSignal(quietPeriod)
	cases = append(cases, check(CaseStopsListening, stopped && !late,
		"no signal for a change signal that arrives after shutdown",
		fmt.Sprintf("stopped: %v, signalled after shutdown: %v", stopped, late)))

	return Report{Cases: cases}
}
