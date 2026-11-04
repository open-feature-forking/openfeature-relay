package sse

import (
	"context"
	"strconv"
	"strings"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/fetching"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/polling"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
	"github.com/stretchr/testify/require"
)

const wait = 5 * time.Second

func create(t *testing.T, config map[string]any) sync.Trigger {
	t.Helper()
	trigger, err := Factory{}.CreateTrigger(sync.Settings{
		Source: sync.SourceConfig{URI: "https://flags.example.test", Provider: "flagsmith"},
		Logger: logger.NewLogger(nil, false),
		Config: config,
	})
	require.NoError(t, err)
	return trigger
}

// counter is a signal function: it never blocks, and counts.
type counter struct{ n atomic.Int32 }

func (c *counter) signal() { c.n.Add(1) }

// runOnce runs the trigger once and returns what Run returned.
func runOnce(ctx context.Context, trigger sync.Trigger, signal func()) <-chan error {
	done := make(chan error, 1)
	go func() { done <- trigger.Run(ctx, signal) }()
	return done
}

// runAgain runs the trigger the way the fetching sync provider does: again after an error, until ctx is done. It
// returns the errors Run returned.
func runAgain(ctx context.Context, trigger sync.Trigger, signal func()) *errorLog {
	log := &errorLog{}
	go func() {
		for ctx.Err() == nil {
			if err := trigger.Run(ctx, signal); err != nil && ctx.Err() == nil {
				log.add(err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	return log
}

type errorLog struct {
	mu   gosync.Mutex
	errs []error
}

func (l *errorLog) add(err error) { l.mu.Lock(); l.errs = append(l.errs, err); l.mu.Unlock() }

func (l *errorLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var parts []string
	for _, err := range l.errs {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, "\n")
}

func connected(t *testing.T, s *stream, n int) {
	t.Helper()
	require.Eventually(t, func() bool { return s.open() == n }, wait, 5*time.Millisecond, "%d open connections", n)
}

func TestSignalsPerEvent(t *testing.T) {
	s := newStream(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var c counter
	runOnce(ctx, create(t, map[string]any{"url": s.URL()}), c.signal)
	connected(t, s, 1)

	s.event(t)
	s.event(t)
	s.send("data: one\n\ndata: two\n\n")

	require.Eventually(t, func() bool { return c.n.Load() == 4 }, wait, 5*time.Millisecond, "one signal for each of the four events")
	require.Never(t, func() bool { return c.n.Load() > 4 }, 150*time.Millisecond, 10*time.Millisecond)
}

func TestConnectingDoesNotSignal(t *testing.T) {
	s := newStream(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var c counter
	runOnce(ctx, create(t, map[string]any{"url": s.URL()}), c.signal)
	connected(t, s, 1)

	require.Never(t, func() bool { return c.n.Load() > 0 }, 150*time.Millisecond, 10*time.Millisecond,
		"a connection is no change; the fetching sync provider fetches once on its own")
}

func TestCommentsDoNotSignal(t *testing.T) {
	s := newStream(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var c counter
	runOnce(ctx, create(t, map[string]any{"url": s.URL()}), c.signal)
	connected(t, s, 1)

	s.send(": keepalive\n\n")
	s.send(":\n\n")
	s.send("retry: 3000\n\n")
	s.send("event: update\n\n") // no data: the stream dispatches nothing
	s.send("\n\n")
	require.Never(t, func() bool { return c.n.Load() > 0 }, 200*time.Millisecond, 10*time.Millisecond)

	s.event(t)
	require.Eventually(t, func() bool { return c.n.Load() == 1 }, wait, 5*time.Millisecond)
}

func TestReconnectsAfterClose(t *testing.T) {
	s := newStream(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var c counter
	trigger := create(t, map[string]any{"url": s.URL()})

	// Run returns an error when the server ends the stream, so that the provider backs off and runs it again
	first := runOnce(ctx, trigger, c.signal)
	connected(t, s, 1)
	s.end(t)
	select {
	case err := <-first:
		require.ErrorContains(t, err, "closed by the server")
	case <-time.After(wait):
		t.Fatal("Run did not return when the server ended the stream")
	}

	log := runAgain(ctx, trigger, c.signal)
	connected(t, s, 1)
	s.event(t)
	require.Eventually(t, func() bool { return c.n.Load() == 1 }, wait, 5*time.Millisecond, "an event on the new connection")

	// a broken connection is an error too, and the trigger connects again after it
	s.drop(t)
	require.Eventually(t, func() bool { return log.text() != "" }, wait, 5*time.Millisecond)
	require.Eventually(t, func() bool { return s.connections.Load() >= 3 }, wait, 5*time.Millisecond, "it connects again")
}

func TestStartsWhenStreamUnavailable(t *testing.T) {
	s := newStream(t)
	s.status.Store(503)
	trigger := create(t, map[string]any{"url": s.URL()})

	_, initializing := trigger.(sync.InitializingTrigger)
	require.False(t, initializing, "the trigger connects in Run, so an unavailable stream never stops startup")

	var c counter
	err := <-runOnce(context.Background(), trigger, c.signal)
	require.ErrorContains(t, err, "503")
	require.Zero(t, c.n.Load())
}

func TestRefusedAndMisconfiguredStreamsAreErrors(t *testing.T) {
	s := newStream(t)
	ctx := context.Background()
	trigger := create(t, map[string]any{"url": s.URL(), "headers": map[string]any{"X-Environment-Key": "key-value-1234"}})

	s.status.Store(401)
	err := <-runOnce(ctx, trigger, func() {})
	require.ErrorContains(t, err, "401")

	s.status.Store(204)
	err = <-runOnce(ctx, trigger, func() {})
	require.ErrorContains(t, err, "204")

	s.status.Store(0)
	s.contentType.Store("text/html")
	err = <-runOnce(ctx, trigger, func() {})
	require.ErrorContains(t, err, "text/html")
	require.ErrorContains(t, err, "text/event-stream")
}

func TestErrorsRevealNeitherHeadersNorQuery(t *testing.T) {
	s := newStream(t)
	trigger := create(t, map[string]any{"url": s.URL(), "headers": map[string]any{"X-Environment-Key": "key-value-1234"}})

	for _, step := range []struct {
		name    string
		prepare func()
	}{
		{"refused", func() { s.status.Store(403) }},
		{"not a stream", func() { s.status.Store(0); s.contentType.Store("text/plain") }},
		{"unreachable", func() { s.srv.Close() }},
	} {
		t.Run(step.name, func(t *testing.T) {
			step.prepare()
			err := <-runOnce(context.Background(), trigger, func() {})
			require.Error(t, err)
			require.NotContains(t, err.Error(), "key-value-1234")
			require.NotContains(t, err.Error(), "secret-in-the-query")
			require.NotContains(t, err.Error(), "token=")
		})
	}
}

func TestSendsTheStreamHeaders(t *testing.T) {
	s := newStream(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	trigger := create(t, map[string]any{"url": s.URL(), "headers": map[string]string{"X-Environment-Key": "key-value-1234"}})
	runOnce(ctx, trigger, func() {})
	connected(t, s, 1)

	header := s.lastHeader.Load().(map[string][]string)
	require.Equal(t, []string{"text/event-stream"}, header["Accept"])
	require.Equal(t, []string{"no-cache"}, header["Cache-Control"])
	require.Equal(t, []string{"key-value-1234"}, header["X-Environment-Key"])
	require.Equal(t, "token=secret-in-the-query", s.lastQuery.Load())
}

func TestARedirectIsNotFollowed(t *testing.T) {
	elsewhere := newStream(t)
	s := newStream(t)
	s.redirect.Store(elsewhere.srv.URL + "/events")
	trigger := create(t, map[string]any{"url": s.URL(), "headers": map[string]any{"X-Environment-Key": "key-value-1234"}})

	err := <-runOnce(context.Background(), trigger, func() {})

	require.ErrorContains(t, err, "302")
	require.Zero(t, elsewhere.connections.Load(), "the headers, which carry a credential, are not sent to another address")
}

func TestRunReturnsNilOnShutdown(t *testing.T) {
	s := newStream(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := runOnce(ctx, create(t, map[string]any{"url": s.URL()}), func() {})
	connected(t, s, 1)

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(wait):
		t.Fatal("Run did not return after the context was done")
	}
	require.Eventually(t, func() bool { return s.open() == 0 }, wait, 5*time.Millisecond, "the connection is closed")
}

func TestShutdownBeforeTheStreamAnswers(t *testing.T) {
	s := newStream(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.NoError(t, <-runOnce(ctx, create(t, map[string]any{"url": s.URL()}), func() {}))
}

// signal is called from the read loop and never waits for anything, so a burst of events is read as fast as the
// connection delivers it, and shutdown still ends Run at once.
func TestSignalNeverBlocks(t *testing.T) {
	s := newStream(t)
	ctx, cancel := context.WithCancel(context.Background())
	var c counter
	done := runOnce(ctx, create(t, map[string]any{"url": s.URL()}), c.signal)
	connected(t, s, 1)

	const events = 5000
	var burst strings.Builder
	for i := 0; i < events; i++ {
		burst.WriteString("data: " + strconv.Itoa(i) + "\n\n")
		if i%500 == 499 {
			s.send(burst.String())
			burst.Reset()
		}
	}
	require.Eventually(t, func() bool { return c.n.Load() == events }, wait, 5*time.Millisecond, "every event of the burst signalled")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(wait):
		t.Fatal("Run did not return")
	}
}

func TestContract(t *testing.T) {
	s := newStream(t)
	synctest.RunTrigger(t, Factory{}, synctest.TriggerInputs{
		Source:    sync.SourceConfig{URI: "https://flags.example.test"},
		Config:    map[string]any{"url": s.URL()},
		Signal:    s.event,
		Interrupt: s.drop,
	})
}

// fetchCounter is a fetcher whose every fetch sees a changed source.
type fetchCounter struct{ calls atomic.Int32 }

func (f *fetchCounter) Fetch(context.Context, string) (sync.FetchResult, error) {
	n := f.calls.Add(1)
	return sync.FetchResult{Data: []byte(`{"flags":{}}`), Extension: ".json", Version: strconv.Itoa(int(n))}, nil
}

// startProvider runs a fetching sync provider over the fetcher with the triggers, and drains its deliveries.
func startProvider(t *testing.T, fetcher sync.Fetcher, triggers ...sync.Trigger) *fetching.Provider {
	t.Helper()
	provider := fetching.New(fetching.Options{
		Fetcher: fetcher, Triggers: triggers, Logger: logger.NewLogger(nil, false),
		Source: "https://flags.example.test", SourceType: "test", Interval: 200 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, provider.Init(ctx), "initializing does not need the stream")
	deliveries := make(chan sync.DataSync, 16)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_ = provider.Sync(ctx, deliveries)
	}()
	go func() {
		for {
			select {
			case <-deliveries:
			case <-ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-finished
	})
	return provider
}

func poller(t *testing.T, seconds uint32) sync.Trigger {
	t.Helper()
	p, err := polling.NewIntervalTrigger(seconds, "")
	require.NoError(t, err)
	return p
}

// An event makes the provider fetch at once: the poller, whose interval is a day here, did not.
func TestAnEventMakesTheProviderFetch(t *testing.T) {
	s := newStream(t)
	fetcher := &fetchCounter{}
	provider := startProvider(t, fetcher, poller(t, polling.MaxInterval), create(t, map[string]any{"url": s.URL()}))
	require.Eventually(t, provider.IsReady, wait, 5*time.Millisecond)
	connected(t, s, 1)
	before := fetcher.calls.Load()

	s.event(t)

	require.Eventually(t, func() bool { return fetcher.calls.Load() == before+1 }, wait, 5*time.Millisecond)
}

// The stream is best effort: with the poller next to it, the source is fetched on schedule whether the stream is
// down or up and silent, and readiness never waits for it.
func TestPollStillRunsWithSSE(t *testing.T) {
	for _, step := range []struct {
		name    string
		prepare func(*stream)
	}{
		{"the stream is down", func(s *stream) { s.status.Store(503) }},
		{"the stream is up and silent", func(*stream) {}},
	} {
		t.Run(step.name, func(t *testing.T) {
			s := newStream(t)
			step.prepare(s)
			fetcher := &fetchCounter{}
			provider := startProvider(t, fetcher, poller(t, 1), create(t, map[string]any{"url": s.URL()}))

			require.Eventually(t, provider.IsReady, wait, 5*time.Millisecond, "ready without the stream")
			require.Eventually(t, func() bool { return fetcher.calls.Load() >= 3 }, 10*time.Second, 20*time.Millisecond,
				"the poller fetches next to the stream: the first fetch and two polls")
		})
	}
}

func TestTheStreamComesBack(t *testing.T) {
	s := newStream(t)
	s.status.Store(503)
	fetcher := &fetchCounter{}
	provider := startProvider(t, fetcher, poller(t, polling.MaxInterval), create(t, map[string]any{"url": s.URL()}))
	require.Eventually(t, provider.IsReady, wait, 5*time.Millisecond)
	require.Eventually(t, func() bool { return s.connections.Load() >= 1 }, wait, 5*time.Millisecond, "it tried")

	s.status.Store(0)
	connected(t, s, 1) // the provider backs off and runs the trigger again

	before := fetcher.calls.Load()
	s.event(t)
	require.Eventually(t, func() bool { return fetcher.calls.Load() > before }, wait, 5*time.Millisecond)
}

func TestSettings(t *testing.T) {
	valid := map[string]any{"url": "https://flags.example.test/events"}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for key, value := range valid {
			m[key] = value
		}
		m[k] = v
		return m
	}
	tests := map[string]struct {
		config map[string]any
		err    string
	}{
		"a url":                       {valid, ""},
		"headers as any":              {with("headers", map[string]any{"X-Environment-Key": "v"}), ""},
		"headers as strings":          {with("headers", map[string]string{"X-Environment-Key": "v"}), ""},
		"no headers":                  {with("headers", nil), ""},
		"http":                        {with("url", "http://localhost:8080/events"), ""},
		"no settings":                 {nil, `"url"`},
		"no url":                      {map[string]any{"headers": map[string]any{}}, `"url"`},
		"a url that is no text":       {with("url", 5), `"url" must be text`},
		"an empty url":                {with("url", " "), `"url"`},
		"another scheme":              {with("url", "ftp://flags.example.test/events"), "http or https"},
		"no host":                     {with("url", "https:///events"), "host"},
		"a url that does not parse":   {with("url", "https://a b/c"), `"url"`},
		"headers that are no mapping": {with("headers", "X-Key: v"), `"headers" must be a mapping`},
		"a header value that is no text": {
			with("headers", map[string]any{"X-Environment-Key": 7}), `header "X-Environment-Key" must be text`,
		},
		"a header name with a space": {with("headers", map[string]any{"X Key": "v"}), `header "X Key"`},
		"an empty header name":       {with("headers", map[string]any{"": "v"}), "header name"},
		"a header value with a line break": {
			with("headers", map[string]any{"X-Key": "v\r\nInjected: 1"}), `header "X-Key"`,
		},
		"a setting nothing reads": {with("header", map[string]any{"X-Key": "v"}), `unknown setting "header" (allowed: headers, url)`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Factory{}.CreateTrigger(sync.Settings{Config: tt.config, Logger: logger.NewLogger(nil, false)})
			if tt.err == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.ErrorContains(t, err, tt.err)
			require.NotContains(t, err.Error(), "Injected", "an error names the header, never its value")
		})
	}
}

func TestSettingsErrorsNameNoHeaderValue(t *testing.T) {
	_, err := Factory{}.CreateTrigger(sync.Settings{Config: map[string]any{
		"url": "https://flags.example.test", "headers": map[string]any{"X-Environment-Key": 12345678},
	}})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "12345678")
}

func TestFactoryDeclarations(t *testing.T) {
	var types []string
	for _, f := range NewFactories() {
		types = append(types, f.Type())
		require.NoError(t, sync.ValidateDeclarations(f))
		_, isTrigger := f.(sync.TriggerFactory)
		require.True(t, isTrigger)
	}
	require.Equal(t, []string{"sse"}, types)
}
