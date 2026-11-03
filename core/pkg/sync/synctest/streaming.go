package synctest

import (
	"context"
	"fmt"
	gosync "sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
)

// StreamingInputs are the test inputs a streaming sync provider's contract needs.
type StreamingInputs struct {
	// Source is a source the provider can connect to. Its URI is the source every delivery has to name.
	Source sync.SourceConfig
	// Expect is the flag configuration the source holds at the start.
	Expect string
	// Change makes the source push a changed flag configuration.
	Change Input
	// Break makes the provider lose the source; Restore brings the source back.
	Break   Input
	Restore Input
	// Remove removes the source's flag configuration, for instance by deleting the file or resource behind it.
	// OnRemove declares what the provider delivers then: Keep delivers nothing, so the last accepted flags stay in
	// force; Empty delivers an empty flag configuration.
	Remove   Input
	OnRemove RemoveBehaviour
	// Timeout is the time limit of a case; zero means DefaultTimeout. Reconnecting may take a multiple.
	Timeout time.Duration
}

// Names of the contract cases of a streaming sync provider (UC-008 BR-002).
const (
	CaseDeliversFirst       = "delivers the source's complete flag configuration first"
	CaseDeliversUnchanged   = "delivers the flag configuration unchanged"
	CaseNamesSource         = "names its source"
	CaseReportsReadiness    = "reports readiness"
	CaseDeliversChange      = "delivers each change the source pushes"
	CaseDeliversOnRequest   = "delivers the complete flag configuration again on request"
	CaseReconnectsToSource  = "reconnects after losing the source"
	CaseSourceRemoved       = "delivers what its source's removal calls for"
	CaseStopsOnShutdown     = "stops on shutdown"
	reconnectTimeoutFactors = 3
)

// RunStreaming runs the contract tests of a streaming sync provider: one subtest per contract case.
func RunStreaming(t *testing.T, f sync.StreamingFactory, in StreamingInputs) {
	t.Helper()
	CheckStreaming(t, f, in).report(t)
}

// collector receives what a provider delivers and keeps all of it.
type collector struct {
	mu         gosync.Mutex
	deliveries []sync.DataSync
	changed    chan struct{}
}

func newCollector(ch <-chan sync.DataSync, done <-chan struct{}) *collector {
	c := &collector{changed: make(chan struct{}, 1)}
	go func() {
		for {
			select {
			case d := <-ch:
				c.mu.Lock()
				c.deliveries = append(c.deliveries, d)
				c.mu.Unlock()
				select {
				case c.changed <- struct{}{}:
				default:
				}
			case <-done:
				return
			}
		}
	}()
	return c
}

// await waits for the delivery with the given index.
func (c *collector) await(index int, d time.Duration) (sync.DataSync, bool) {
	deadline := time.After(d)
	for {
		c.mu.Lock()
		if index < len(c.deliveries) {
			delivery := c.deliveries[index]
			c.mu.Unlock()
			return delivery, true
		}
		c.mu.Unlock()
		select {
		case <-c.changed:
		case <-deadline:
			return sync.DataSync{}, false
		}
	}
}

// CheckStreaming runs the contract tests of a streaming sync provider and returns their outcome. t is only handed to
// the test inputs.
func CheckStreaming(t testing.TB, f sync.StreamingFactory, in StreamingInputs) Report {
	if msg := missing(
		missingInput{"StreamingInputs.Expect", in.Expect != "", []string{CaseDeliversUnchanged, CaseDeliversChange}},
		missingInput{"StreamingInputs.Change", in.Change != nil, []string{CaseDeliversChange}},
		missingInput{"StreamingInputs.Break", in.Break != nil, []string{CaseReconnectsToSource}},
		missingInput{"StreamingInputs.Restore", in.Restore != nil, []string{CaseReconnectsToSource}},
		missingInput{"StreamingInputs.Remove", in.Remove != nil, []string{CaseSourceRemoved}},
		missingInput{"StreamingInputs.OnRemove", in.OnRemove == Keep || in.OnRemove == Empty, []string{CaseSourceRemoved}},
	); msg != "" {
		return Report{Aborted: msg}
	}
	if msg := checkSchemes(f); msg != "" {
		return Report{Aborted: msg}
	}
	provider, err := f.CreateStreaming(newSettings(in.Source, f.Type(), nil))
	if err != nil || provider == nil {
		return Report{Aborted: fmt.Sprintf("the factory cannot create the streaming sync provider (provider %v, error %v)", provider, err)}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := provider.Init(ctx); err != nil {
		return Report{Aborted: fmt.Sprintf("the streaming sync provider cannot be initialized: %v", err)}
	}
	deliveries := make(chan sync.DataSync, 64)
	stopCollecting := make(chan struct{})
	defer close(stopCollecting)
	received := newCollector(deliveries, stopCollecting)
	syncResult := make(chan error, 1)
	go func() { syncResult <- provider.Sync(ctx, deliveries) }()

	limit := timeoutOr(in.Timeout)
	var cases []Case
	next := 0 // the index of the next delivery a case waits for

	first, got := received.await(next, limit)
	next++
	cases = append(cases, check(CaseDeliversFirst, got,
		fmt.Sprintf("the complete flag configuration within %s", limit),
		fmt.Sprintf("a delivery arrived: %v", got)))
	cases = append(cases, check(CaseDeliversUnchanged, got && sameJSON(first.FlagData, in.Expect),
		fmt.Sprintf("the delivered flag configuration equals %s", in.Expect),
		fmt.Sprintf("delivered %s", first.FlagData)))

	ready := false
	deadline := time.Now().Add(limit)
	for !ready && time.Now().Before(deadline) {
		ready = provider.IsReady()
		if !ready {
			time.Sleep(5 * time.Millisecond)
		}
	}
	cases = append(cases, check(CaseReportsReadiness, ready,
		fmt.Sprintf("IsReady is true within %s of the first delivery", limit), fmt.Sprintf("ready: %v", ready)))

	in.Change(t)
	changed, next, got := received.awaitMatch(next, func(d sync.DataSync) bool { return !sameJSON(d.FlagData, in.Expect) }, limit)
	cases = append(cases, check(CaseDeliversChange, got && !sameJSON(changed.FlagData, in.Expect),
		"a delivery of the changed flag configuration",
		fmt.Sprintf("a delivery arrived: %v, delivered %s", got, changed.FlagData)))

	latest := changed.FlagData
	reSyncErr := provider.ReSync(ctx, deliveries)
	again, next, got := received.awaitMatch(next, func(d sync.DataSync) bool { return sameJSON(d.FlagData, latest) }, limit)
	cases = append(cases, check(CaseDeliversOnRequest, reSyncErr == nil && got && sameJSON(again.FlagData, latest),
		fmt.Sprintf("ReSync returns nil and delivers the complete flag configuration, %s", latest),
		fmt.Sprintf("ReSync returned %v, a delivery arrived: %v, delivered %s", reSyncErr, got, again.FlagData)))

	in.Break(t)
	in.Restore(t)
	reconnected, next, got := received.awaitMatch(next, func(d sync.DataSync) bool { return sameJSON(d.FlagData, latest) }, limit*reconnectTimeoutFactors)
	cases = append(cases, check(CaseReconnectsToSource, got && sameJSON(reconnected.FlagData, latest),
		fmt.Sprintf("after the source was lost and came back, a delivery of the complete flag configuration, %s", latest),
		fmt.Sprintf("a delivery arrived: %v, delivered %s", got, reconnected.FlagData)))

	in.Remove(t)
	removed, _, got := received.awaitMatch(next, func(d sync.DataSync) bool { return in.OnRemove == Keep || sameJSON(d.FlagData, "{}") }, quietPeriodFor(in.OnRemove, limit))
	switch in.OnRemove {
	case Keep:
		cases = append(cases, check(CaseSourceRemoved, !got,
			"a removed source delivers nothing (Keep): the last accepted flags stay in force",
			fmt.Sprintf("a delivery arrived: %v, delivered %s", got, removed.FlagData)))
	case Empty:
		cases = append(cases, check(CaseSourceRemoved, got && sameJSON(removed.FlagData, "{}"),
			"a removed source delivers an empty flag configuration (Empty)",
			fmt.Sprintf("a delivery arrived: %v, delivered %s", got, removed.FlagData)))
	}

	cancel()
	var syncErr error
	stopped := false
	select {
	case syncErr = <-syncResult:
		stopped = true
	case <-time.After(limit):
	}
	cases = append(cases, check(CaseStopsOnShutdown, stopped && syncErr == nil,
		fmt.Sprintf("Sync returns nil within %s of the context being done", limit),
		fmt.Sprintf("returned: %v, error: %v", stopped, syncErr)))

	cases = append(cases, namedSource(received, in.Source.URI))
	return Report{Cases: cases}
}

// namedSource checks that every delivery carried the configured URI as its source.
func namedSource(c *collector, uri string) Case {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, d := range c.deliveries {
		if d.Source != uri {
			return check(CaseNamesSource, false,
				fmt.Sprintf("every delivery names the configured source %q", uri),
				fmt.Sprintf("delivery %d names %q", i+1, d.Source))
		}
	}
	return check(CaseNamesSource, len(c.deliveries) > 0,
		fmt.Sprintf("every delivery names the configured source %q", uri), fmt.Sprintf("%d deliveries, all naming it", len(c.deliveries)))
}

// quietPeriodFor is how long to wait for a delivery after a removal.
func quietPeriodFor(onRemove RemoveBehaviour, limit time.Duration) time.Duration {
	if onRemove == Keep {
		return quietPeriod
	}
	return limit
}

// awaitMatch waits for the first delivery from the given index on that satisfies match, and returns it with the
// index after it. A provider may repeat a flag configuration it already delivered before it delivers the one a case
// waits for.
func (c *collector) awaitMatch(from int, match func(sync.DataSync) bool, d time.Duration) (sync.DataSync, int, bool) {
	deadline := time.Now().Add(d)
	for index := from; ; {
		delivery, ok := c.await(index, time.Until(deadline))
		if !ok {
			return sync.DataSync{}, index, false
		}
		index++
		if match(delivery) {
			return delivery, index, true
		}
	}
}
