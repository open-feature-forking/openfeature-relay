package synctest_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
	"github.com/stretchr/testify/require"
)

const (
	stubScheme = "stub://"
	stubURI    = stubScheme + "flags"
	flagsOne   = `{"flags":{"one":{}}}`
	flagsTwo   = `{"flags":{"two":{}}}`
	shortLimit = 300 * time.Millisecond
)

// ---- fetcher stubs ----

// fetcherBug selects the one thing a stub fetcher gets wrong.
type fetcherBug string

const (
	fetcherOK             fetcherBug = ""
	fetcherAlwaysChanged  fetcherBug = "never reports unchanged"
	fetcherSameVersion    fetcherBug = "returns the same version after a change"
	fetcherDataOnFailure  fetcherBug = "returns a flag configuration when the fetch fails"
	fetcherHangs          fetcherBug = "does not return when its context is done"
	fetcherEmptyUnchanged fetcherBug = "reports unchanged for an empty previous version after the first fetch"
	fetcherNoVersion      fetcherBug = "returns no source version"
)

type memSource struct {
	mu      gosync.Mutex
	data    string
	version int
	broken  bool
}

func (m *memSource) change() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data, m.version = flagsTwo, m.version+1
}

func (m *memSource) breakIt() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broken = true
}

type stubFetcher struct {
	src     *memSource
	bug     fetcherBug
	release chan struct{}
	calls   int
}

func (f *stubFetcher) Fetch(ctx context.Context, previous string) (sync.FetchResult, error) {
	if f.bug == fetcherHangs && ctx.Err() != nil {
		<-f.release
	}
	f.src.mu.Lock()
	defer f.src.mu.Unlock()

	version := fmt.Sprint("v", f.src.version)
	if f.bug == fetcherNoVersion {
		version = ""
	}
	if f.bug == fetcherSameVersion {
		version = "v0"
	}
	if f.src.broken {
		if f.bug == fetcherDataOnFailure {
			return sync.FetchResult{Data: []byte(f.src.data), Version: version}, nil
		}
		return sync.FetchResult{}, errors.New("the source is down")
	}
	f.calls++
	if f.bug == fetcherEmptyUnchanged && previous == "" && f.calls > 1 {
		return sync.FetchResult{Unchanged: true}, nil
	}
	if previous == version && f.bug != fetcherAlwaysChanged {
		return sync.FetchResult{Unchanged: true}, nil
	}
	return sync.FetchResult{Data: []byte(f.src.data), Extension: ".json", Version: version}, nil
}

type stubFetcherFactory struct {
	typ     string
	schemes []string
	src     *memSource
	bug     fetcherBug
	release chan struct{}
	err     error
}

func (f stubFetcherFactory) Type() string      { return f.typ }
func (f stubFetcherFactory) Schemes() []string { return f.schemes }
func (f stubFetcherFactory) ParseURI(uri string) (sync.SourceConfig, bool) {
	for _, s := range f.schemes {
		if s != "" && strings.HasPrefix(uri, s) {
			return sync.SourceConfig{URI: uri, Provider: f.typ}, true
		}
	}
	return sync.SourceConfig{}, false
}

func (f stubFetcherFactory) CreateFetcher(sync.Settings) (sync.Fetcher, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &stubFetcher{src: f.src, bug: f.bug, release: f.release}, nil
}

func fetcherSetup(t *testing.T, bug fetcherBug) (stubFetcherFactory, synctest.FetcherInputs) {
	t.Helper()
	src := &memSource{data: flagsOne}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	return stubFetcherFactory{typ: "stub", schemes: []string{stubScheme}, src: src, bug: bug, release: release},
		synctest.FetcherInputs{
			Source:  sync.SourceConfig{URI: stubURI},
			Change:  func(testing.TB) { src.change() },
			Break:   func(testing.TB) { src.breakIt() },
			Timeout: shortLimit,
		}
}

func TestFetcherContract(t *testing.T) {
	t.Run("a correct fetcher passes every case", func(t *testing.T) {
		f, in := fetcherSetup(t, fetcherOK)
		report := synctest.CheckFetcher(t, f, in)

		require.Empty(t, report.Aborted)
		require.Empty(t, report.FailedCases())
		require.Len(t, report.Cases, 5)
	})

	tests := []struct {
		bug  fetcherBug
		fail string
	}{
		{fetcherAlwaysChanged, synctest.CaseReportsUnchanged},
		{fetcherEmptyUnchanged, synctest.CaseReportsUnchanged},
		{fetcherSameVersion, synctest.CaseNewVersion},
		{fetcherDataOnFailure, synctest.CaseReportsFailedFetch},
		{fetcherHangs, synctest.CaseAbandonsOnDone},
	}
	for _, tt := range tests {
		t.Run("a fetcher that "+string(tt.bug)+" fails that case only", func(t *testing.T) {
			f, in := fetcherSetup(t, tt.bug)
			report := synctest.CheckFetcher(t, f, in)

			require.Empty(t, report.Aborted)
			require.Equal(t, []string{tt.fail}, report.FailedCases())
			for _, c := range report.Cases {
				if !c.Passed {
					require.NotEmpty(t, c.Expected)
					require.NotEmpty(t, c.Observed)
				}
			}
		})
	}

	t.Run("a missing test input is named with the cases that need it", func(t *testing.T) {
		f, in := fetcherSetup(t, fetcherOK)
		in.Change = nil
		report := synctest.CheckFetcher(t, f, in)

		require.Contains(t, report.Aborted, "FetcherInputs.Change")
		require.Contains(t, report.Aborted, synctest.CaseNewVersion)
		require.Empty(t, report.Cases)
		require.True(t, report.Failed())
	})

	t.Run("an empty scheme is rejected and named", func(t *testing.T) {
		f, in := fetcherSetup(t, fetcherOK)
		f.schemes = []string{stubScheme, ""}
		report := synctest.CheckFetcher(t, f, in)

		require.Contains(t, report.Aborted, "scheme 2 is empty")
		require.Empty(t, report.Cases)
	})

	t.Run("an empty type is rejected", func(t *testing.T) {
		f, in := fetcherSetup(t, fetcherOK)
		f.typ = ""
		require.Contains(t, synctest.CheckFetcher(t, f, in).Aborted, "the type name is empty")
	})

	t.Run("ParseURI has to claim the declared schemes", func(t *testing.T) {
		f, in := fetcherSetup(t, fetcherOK)
		f.schemes = []string{stubScheme, "other://"}
		f.typ = "stub"
		report := synctest.CheckFetcher(t, mismatched{f}, in)

		require.Contains(t, report.Aborted, `"other://"`)
	})

	t.Run("a factory that cannot create the fetcher aborts the run", func(t *testing.T) {
		f, in := fetcherSetup(t, fetcherOK)
		f.err = errors.New("no credentials")
		report := synctest.CheckFetcher(t, f, in)

		require.Contains(t, report.Aborted, "no credentials")
		require.Empty(t, report.Cases)
	})
}

// mismatched is a factory whose ParseURI does not claim the second declared scheme.
type mismatched struct{ stubFetcherFactory }

func (m mismatched) ParseURI(uri string) (sync.SourceConfig, bool) {
	if strings.HasPrefix(uri, "other://") {
		return sync.SourceConfig{}, false
	}
	return m.stubFetcherFactory.ParseURI(uri)
}

// ---- fetch trigger stubs ----

type triggerBug string

const (
	triggerOK             triggerBug = ""
	triggerNoReconnect    triggerBug = "stops for good when it loses the change signal"
	triggerIgnoresDone    triggerBug = "does not return when its context is done"
	triggerErrorOnDone    triggerBug = "returns an error when its context is done"
	triggerSignalsAlways  triggerBug = "keeps signalling after shutdown"
	triggerSignalsUnasked triggerBug = "signals without a change signal"
	triggerDropsSignals   triggerBug = "drops every second change signal"
)

type stubTrigger struct {
	bug     triggerBug
	seen    int
	events  chan string // "signal" or "lose"
	release chan struct{}
	late    chan func()
}

func (s *stubTrigger) Run(ctx context.Context, signal func()) error {
	if s.bug == triggerSignalsUnasked {
		signal()
	}
	for {
		select {
		case <-ctx.Done():
			switch s.bug {
			case triggerIgnoresDone:
				<-s.release
			case triggerErrorOnDone:
				return ctx.Err()
			case triggerSignalsAlways:
				s.late <- signal
			}
			return nil
		case ev := <-s.events:
			switch ev {
			case "signal":
				if s.bug == triggerDropsSignals {
					s.seen++
					if s.seen > 1 {
						continue
					}
				}
				signal()
			case "lose":
				if s.bug == triggerNoReconnect {
					return nil
				}
				return errors.New("lost the change signal")
			}
		}
	}
}

type stubTriggerFactory struct {
	typ string
	t   *stubTrigger
	err error
}

func (f stubTriggerFactory) Type() string { return f.typ }
func (f stubTriggerFactory) CreateTrigger(sync.Settings) (sync.Trigger, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.t, nil
}

func triggerSetup(t *testing.T, bug triggerBug) (stubTriggerFactory, synctest.TriggerInputs) {
	t.Helper()
	st := &stubTrigger{bug: bug, events: make(chan string, 8), release: make(chan struct{}), late: make(chan func(), 1)}
	t.Cleanup(func() { close(st.release) })
	return stubTriggerFactory{typ: "stub-trigger", t: st}, synctest.TriggerInputs{
		Signal:    func(testing.TB) { st.events <- "signal" },
		Interrupt: func(testing.TB) { st.events <- "lose" },
		Timeout:   shortLimit,
	}
}

func TestTriggerContract(t *testing.T) {
	t.Run("a correct trigger passes every case", func(t *testing.T) {
		f, in := triggerSetup(t, triggerOK)
		report := synctest.CheckTrigger(t, f, in)

		require.Empty(t, report.Aborted)
		require.Empty(t, report.FailedCases())
		require.Len(t, report.Cases, 5)
	})

	tests := []struct {
		bug  triggerBug
		fail []string
	}{
		{triggerNoReconnect, []string{synctest.CaseReconnects, synctest.CaseReturnsNilOnDone}},
		{triggerIgnoresDone, []string{synctest.CaseReturnsNilOnDone, synctest.CaseStopsListening}},
		{triggerErrorOnDone, []string{synctest.CaseReturnsNilOnDone}},
		{triggerSignalsUnasked, []string{synctest.CaseNeverFetchesItself}},
	}
	for _, tt := range tests {
		t.Run("a trigger that "+string(tt.bug)+" fails its cases", func(t *testing.T) {
			f, in := triggerSetup(t, tt.bug)
			report := synctest.CheckTrigger(t, f, in)

			require.Empty(t, report.Aborted)
			require.ElementsMatch(t, tt.fail, report.FailedCases())
		})
	}

	t.Run("missing test inputs are named with the cases that need them", func(t *testing.T) {
		f, in := triggerSetup(t, triggerOK)
		in.Interrupt = nil
		report := synctest.CheckTrigger(t, f, in)

		require.Contains(t, report.Aborted, "TriggerInputs.Interrupt")
		require.Contains(t, report.Aborted, synctest.CaseReconnects)
		require.NotContains(t, report.Aborted, "TriggerInputs.Signal")
	})

	t.Run("an empty type is rejected", func(t *testing.T) {
		f, in := triggerSetup(t, triggerOK)
		f.typ = ""
		require.Contains(t, synctest.CheckTrigger(t, f, in).Aborted, "the type name is empty")
	})

	t.Run("a factory that cannot create the trigger aborts the run", func(t *testing.T) {
		f, in := triggerSetup(t, triggerOK)
		f.err = errors.New("no endpoint")
		report := synctest.CheckTrigger(t, f, in)

		require.Contains(t, report.Aborted, "no endpoint")
		require.Empty(t, report.Cases)
	})
}

// ---- streaming sync provider stubs ----

type streamBug string

const (
	streamOK            streamBug = ""
	streamWrongSource   streamBug = "names another source"
	streamNoReconnect   streamBug = "does not deliver again after the source came back"
	streamNeverReady    streamBug = "never reports readiness"
	streamIgnoresDone   streamBug = "does not return when its context is done"
	streamDeliversOnDel streamBug = "delivers when its source is removed although it declares Keep"
	streamNoReSync      streamBug = "does not deliver on ReSync"
	streamExitsWhenDown streamBug = "returns an error when the source is unavailable at startup"
	streamIgnoresChange streamBug = "does not deliver a change the source pushes"
	streamAltersData    streamBug = "delivers a flag configuration other than its source's"
)

type stubStream struct {
	uri     string
	bug     streamBug
	events  chan string
	release chan struct{}
	ready   chan struct{}
	down    *atomic.Bool

	mu      gosync.Mutex
	current string
}

func (s *stubStream) source() string {
	if s.bug == streamWrongSource {
		return "stub://other"
	}
	return s.uri
}

func (s *stubStream) Init(context.Context) error { return nil }

func (s *stubStream) IsReady() bool {
	if s.down.Load() {
		return false
	}
	if s.bug == streamNeverReady {
		return false
	}
	select {
	case <-s.ready:
		return true
	default:
		return false
	}
}

func (s *stubStream) snapshot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

func (s *stubStream) set(flags string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = flags
}

func (s *stubStream) ReSync(_ context.Context, ch chan<- sync.DataSync) error {
	if s.bug == streamNoReSync {
		return nil
	}
	ch <- sync.DataSync{FlagData: s.snapshot(), Source: s.source()}
	return nil
}

func (s *stubStream) Sync(ctx context.Context, ch chan<- sync.DataSync) error {
	for s.down.Load() {
		if s.bug == streamExitsWhenDown {
			return errors.New("the source is unavailable")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Millisecond):
		}
	}
	s.set(flagsOne)
	first := s.snapshot()
	if s.bug == streamAltersData {
		first = flagsTwo
	}
	ch <- sync.DataSync{FlagData: first, Source: s.source()}
	close(s.ready)
	for {
		select {
		case <-ctx.Done():
			if s.bug == streamIgnoresDone {
				<-s.release
			}
			return nil
		case ev := <-s.events:
			switch ev {
			case "change":
				s.set(flagsTwo)
				if s.bug == streamIgnoresChange {
					continue
				}
				ch <- sync.DataSync{FlagData: s.snapshot(), Source: s.source()}
			case "restore":
				if s.bug != streamNoReconnect {
					ch <- sync.DataSync{FlagData: s.snapshot(), Source: s.source()}
				}
			case "remove":
				if s.bug == streamDeliversOnDel {
					ch <- sync.DataSync{FlagData: "{}", Source: s.source()}
				}
			}
		}
	}
}

type stubStreamFactory struct {
	typ     string
	schemes []string
	// the world every provider the factory creates shares: its events, its availability and what releases a hung Sync
	proto *stubStream
	err   error
}

func (f stubStreamFactory) Type() string      { return f.typ }
func (f stubStreamFactory) Schemes() []string { return f.schemes }
func (f stubStreamFactory) ParseURI(uri string) (sync.SourceConfig, bool) {
	if strings.HasPrefix(uri, stubScheme) {
		return sync.SourceConfig{URI: uri, Provider: f.typ}, true
	}
	return sync.SourceConfig{}, false
}

func (f stubStreamFactory) CreateStreaming(sync.Settings) (sync.StreamingSyncProvider, error) {
	if f.err != nil {
		return nil, f.err
	}
	p := f.proto
	return &stubStream{uri: p.uri, bug: p.bug, events: p.events, release: p.release, down: p.down, ready: make(chan struct{})}, nil
}

func streamSetup(t *testing.T, bug streamBug) (stubStreamFactory, synctest.StreamingInputs) {
	t.Helper()
	proto := &stubStream{uri: stubURI, bug: bug, events: make(chan string, 8), release: make(chan struct{}), down: new(atomic.Bool)}
	t.Cleanup(func() { close(proto.release) })
	send := func(ev string) synctest.Input { return func(testing.TB) { proto.events <- ev } }
	return stubStreamFactory{typ: "stub", schemes: []string{stubScheme}, proto: proto}, synctest.StreamingInputs{
		Source:      sync.SourceConfig{URI: stubURI},
		Expect:      flagsOne,
		Change:      send("change"),
		Break:       send("break"),
		Restore:     send("restore"),
		Remove:      send("remove"),
		OnRemove:    synctest.Keep,
		Unavailable: func(testing.TB) { proto.down.Store(true) },
		Available:   func(testing.TB) { proto.down.Store(false) },
		Timeout:     shortLimit,
	}
}

func TestStreamingContract(t *testing.T) {
	t.Run("a correct provider passes every case", func(t *testing.T) {
		f, in := streamSetup(t, streamOK)
		report := synctest.CheckStreaming(t, f, in)

		require.Empty(t, report.Aborted)
		require.Empty(t, report.FailedCases())
		require.Len(t, report.Cases, 10)
	})

	// the startup case delivers, reports ready and shuts down too, so a provider that breaks one of those fails it as well
	tests := []struct {
		bug  streamBug
		fail []string
	}{
		{streamWrongSource, []string{synctest.CaseNamesSource, synctest.CaseSurvivesFirstFailure}},
		{streamNoReconnect, []string{synctest.CaseReconnectsToSource}},
		{streamNeverReady, []string{synctest.CaseReportsReadiness, synctest.CaseSurvivesFirstFailure}},
		{streamIgnoresDone, []string{synctest.CaseStopsOnShutdown, synctest.CaseSurvivesFirstFailure}},
		{streamDeliversOnDel, []string{synctest.CaseSourceRemoved}},
		{streamNoReSync, []string{synctest.CaseDeliversOnRequest}},
		{streamExitsWhenDown, []string{synctest.CaseSurvivesFirstFailure}},
	}
	for _, tt := range tests {
		t.Run("a provider that "+string(tt.bug)+" fails its cases", func(t *testing.T) {
			f, in := streamSetup(t, tt.bug)
			report := synctest.CheckStreaming(t, f, in)

			require.Empty(t, report.Aborted)
			require.ElementsMatch(t, tt.fail, report.FailedCases())
		})
	}

	t.Run("a provider that empties its flags on removal passes when it declares Empty", func(t *testing.T) {
		f, in := streamSetup(t, streamDeliversOnDel)
		in.OnRemove = synctest.Empty
		report := synctest.CheckStreaming(t, f, in)

		require.Empty(t, report.Aborted)
		require.Empty(t, report.FailedCases())
	})

	t.Run("a provider that keeps its flags fails when it declares Empty", func(t *testing.T) {
		f, in := streamSetup(t, streamOK)
		in.OnRemove = synctest.Empty
		report := synctest.CheckStreaming(t, f, in)

		require.Equal(t, []string{synctest.CaseSourceRemoved}, report.FailedCases())
	})

	t.Run("a missing test input is named with the cases that need it", func(t *testing.T) {
		f, in := streamSetup(t, streamOK)
		in.Restore = nil
		in.OnRemove = 0
		report := synctest.CheckStreaming(t, f, in)

		require.Contains(t, report.Aborted, "StreamingInputs.Restore")
		require.Contains(t, report.Aborted, "StreamingInputs.OnRemove")
		require.Contains(t, report.Aborted, synctest.CaseReconnectsToSource)
		require.Contains(t, report.Aborted, synctest.CaseSourceRemoved)
		require.Empty(t, report.Cases)
	})

	t.Run("an empty scheme is rejected", func(t *testing.T) {
		f, in := streamSetup(t, streamOK)
		f.schemes = []string{""}
		report := synctest.CheckStreaming(t, f, in)

		require.Contains(t, report.Aborted, "scheme 1 is empty")
	})

	t.Run("a factory that cannot create the provider aborts the run", func(t *testing.T) {
		f, in := streamSetup(t, streamOK)
		f.err = errors.New("no endpoint")
		report := synctest.CheckStreaming(t, f, in)

		require.Contains(t, report.Aborted, "no endpoint")
		require.Empty(t, report.Cases)
	})
}

// TestRunReportsCasesAsSubtests runs a passing stub through the testing.T entry point.
func TestRunReportsCasesAsSubtests(t *testing.T) {
	f, in := fetcherSetup(t, fetcherOK)
	synctest.RunFetcher(t, f, in)

	tf, tin := triggerSetup(t, triggerOK)
	synctest.RunTrigger(t, tf, tin)

	sf, sin := streamSetup(t, streamOK)
	synctest.RunStreaming(t, sf, sin)
}

// TestEveryCaseHasABrokenStub: for the cases the other tests leave to chance, a stub that breaks that rule fails the
// case, and fails it as the author would see it, with the expected and the observed behaviour.
func TestEveryCaseHasABrokenStub(t *testing.T) {
	t.Run("fetcher: fetches once per request with its source version", func(t *testing.T) {
		f, in := fetcherSetup(t, fetcherNoVersion)
		report := synctest.CheckFetcher(t, f, in)

		require.Empty(t, report.Aborted)
		require.Contains(t, report.FailedCases(), synctest.CaseFetchesOnce)
	})
	t.Run("trigger: signals each time the change signal arrives", func(t *testing.T) {
		f, in := triggerSetup(t, triggerDropsSignals)
		report := synctest.CheckTrigger(t, f, in)

		require.Empty(t, report.Aborted)
		require.Contains(t, report.FailedCases(), synctest.CaseSignalsEachTime)
	})
	t.Run("streaming: delivers each change the source pushes", func(t *testing.T) {
		f, in := streamSetup(t, streamIgnoresChange)
		report := synctest.CheckStreaming(t, f, in)

		require.Empty(t, report.Aborted)
		require.Contains(t, report.FailedCases(), synctest.CaseDeliversChange)
	})
	t.Run("streaming: delivers the flag configuration unchanged", func(t *testing.T) {
		f, in := streamSetup(t, streamAltersData)
		report := synctest.CheckStreaming(t, f, in)

		require.Empty(t, report.Aborted)
		require.Contains(t, report.FailedCases(), synctest.CaseDeliversUnchanged)
	})
}
