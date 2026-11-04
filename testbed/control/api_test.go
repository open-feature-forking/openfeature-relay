package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The texts and status codes of the launchpad of flagd-testbed v3.11.0, with the trailing newline of json.Encoder.
func TestAPITexts(t *testing.T) {
	ts := newTestServer(t, &fakeTarget{}, nil)

	tests := []struct {
		path string
		code int
		body string
	}{
		{"/start?config=ssl", 200, `{"status":"success","message":"flagd started successfully"}` + "\n"},
		{"/start", 200, `{"status":"success","message":"flagd started successfully"}` + "\n"},
		{"/stop", 200, `{"status":"success","message":"flagd stopped successfully"}` + "\n"},
		{"/restart?seconds=x", 400, `{"status":"error","message":"'seconds' must be a non-negative integer"}` + "\n"},
		{"/restart?seconds=-1", 400, `{"status":"error","message":"'seconds' must be a non-negative integer"}` + "\n"},
		{"/restart?seconds=0", 200, `{"status":"success","message":"flagd will restart in 0 seconds"}` + "\n"},
		{"/change", 200, `{"status":"success","message":"Default variant successfully changed to 'bar'\n"}` + "\n"},
		{"/change", 200, `{"status":"success","message":"Default variant successfully changed to 'foo'\n"}` + "\n"},
	}
	for _, tt := range tests {
		code, contentType, body := ts.call(t, http.MethodGet, tt.path)
		require.Equal(t, tt.code, code, tt.path)
		require.Equal(t, "application/json", contentType, tt.path)
		require.Equal(t, tt.body, body, tt.path)
	}
}

func TestAPIAnswersAnyMethod(t *testing.T) {
	ts := newTestServer(t, &fakeTarget{}, nil)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		code, _, body := ts.call(t, method, "/stop")
		require.Equal(t, 200, code, method)
		require.Contains(t, body, "flagd stopped successfully", method)
	}
}

func TestStopFailureIsReported(t *testing.T) {
	target := &fakeTarget{stopFn: func(context.Context) error { return errors.New("stuck") }}
	ts := newTestServer(t, target, nil)

	code, body := ts.get(t, "/stop")

	require.Equal(t, 500, code)
	require.Equal(t, `{"status":"error","message":"Failed to stop flagd: stuck"}`+"\n", body)
}

// Q24: an unknown configuration is answered at once, naming it, and nothing is started or stopped.
func TestUnknownConfiguration(t *testing.T) {
	target := &fakeTarget{}
	ts := newTestServer(t, target, nil)
	before := len(target.log())

	for _, name := range []string{"nope", "../default", "ssl/../default"} {
		began := time.Now()
		code, body := ts.get(t, "/start?config="+name)

		require.Less(t, time.Since(began), 100*time.Millisecond, name)
		require.Equal(t, 500, code, name)
		require.Equal(t, fmt.Sprintf(`{"status":"error","message":"Failed to start flagd: unknown configuration %s"}`, jsonQuote(name))+"\n", body)
	}
	require.Len(t, target.log(), before, "no start and no stop: the gate and the runtime stay as they were")
	require.Equal(t, "default", ts.config())
}

func jsonQuote(s string) string { return strings.ReplaceAll(fmt.Sprintf("%q", s), `"`, `\"`) }

// A target that does not have the configuration is answered the same, without a stop.
func TestUnknownConfigurationFromTheTarget(t *testing.T) {
	target := &fakeTarget{startFn: func(_ context.Context, config string) error {
		if config == "ssl" {
			return fmt.Errorf("%w %q", ErrUnknownConfiguration, config)
		}
		return nil
	}}
	ts := newTestServer(t, target, func(c *Config) { c.NoConfigs = true })
	before := len(target.log())

	code, body := ts.get(t, "/start?config=ssl")

	require.Equal(t, 500, code)
	require.Equal(t, `{"status":"error","message":"Failed to start flagd: unknown configuration \"ssl\""}`+"\n", body)
	require.Equal(t, 2, len(target.log())-before, "the start only")
	require.Zero(t, target.count("stop"))
}

// An unknown configuration changes nothing, a pending restart included: it still ends the outage.
func TestUnknownConfigurationKeepsPendingRestart(t *testing.T) {
	target := &fakeTarget{}
	ts := newTestServer(t, target, nil)
	starts := target.count("start-end")

	ts.get(t, "/restart?seconds=1")
	code, _ := ts.get(t, "/start?config=nope")
	require.Equal(t, 500, code)

	require.Eventually(t, func() bool { return target.count("start-end") == starts+1 && target.count("open") == 2 }, 5*time.Second, 10*time.Millisecond,
		"the restart still happens, and opens the target: %v", target.log())
}

// A start that fails to get ready does not cancel a pending restart either.
func TestFailedStartKeepsPendingRestart(t *testing.T) {
	target := &fakeTarget{startFn: func(_ context.Context, config string) error {
		if config == "broken" {
			return errors.New("boom")
		}
		return nil
	}}
	ts := newTestServer(t, target, nil)
	starts := target.count("start-end:default")

	ts.get(t, "/restart?seconds=1")
	code, _ := ts.get(t, "/start?config=broken")
	require.Equal(t, 500, code)

	require.Eventually(t, func() bool { return target.count("start-end:default") == starts+1 }, 5*time.Second, 10*time.Millisecond,
		"the restart still happens: %v", target.log())
}

// Quirk 1 is fixed: a start that fails does not replace the configuration in use.
func TestFailedStartKeepsLastGoodConfiguration(t *testing.T) {
	target := &fakeTarget{startFn: func(_ context.Context, config string) error {
		if config == "broken" {
			return errors.New("boom")
		}
		return nil
	}}
	ts := newTestServer(t, target, nil)

	code, _ := ts.get(t, "/start?config=ssl")
	require.Equal(t, 200, code)
	code, body := ts.get(t, "/start?config=broken")
	require.Equal(t, 500, code)
	require.Equal(t, `{"status":"error","message":"Failed to start flagd: boom"}`+"\n", body)

	code, _ = ts.get(t, "/start")
	require.Equal(t, 200, code)
	code, _ = ts.get(t, "/restart?seconds=0")
	require.Equal(t, 200, code)
	require.Eventually(t, func() bool { return target.count("start-end:ssl") == 3 }, 5*time.Second, 10*time.Millisecond,
		"a bare /start and a /restart start the last good configuration: %v", target.log())
	require.Equal(t, 1, target.count("start-begin:broken"), "broken was tried once")
}

// A8: a start that does not get ready in time answers 500 "health check timed out" and leaves the target closed.
func TestStartNotReadyIn10s(t *testing.T) {
	target := &fakeTarget{startFn: func(ctx context.Context, config string) error {
		if config == "broken" {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}
	ts := newTestServer(t, target, func(c *Config) { c.StartTimeout = 100 * time.Millisecond })
	stops := target.count("stop")

	began := time.Now()
	code, body := ts.get(t, "/start?config=broken")

	require.GreaterOrEqual(t, time.Since(began), 100*time.Millisecond)
	require.Equal(t, 500, code)
	require.Equal(t, `{"status":"error","message":"Failed to start flagd: flagd health check timed out"}`+"\n", body)
	require.Equal(t, stops+1, target.count("stop"), "the target is closed again")
	require.Equal(t, "default", ts.config())
}

func TestStartThatReturnsNotReadyIsAFailure(t *testing.T) {
	target := &fakeTarget{}
	ts := newTestServer(t, target, nil)
	target.notReady.Store(true)

	code, body := ts.get(t, "/start")

	require.Equal(t, 500, code)
	require.Contains(t, body, "flagd health check timed out")
}

// A4: after /stop the target stays closed, and a pending restart does not start it again.
func TestStopUntilStart(t *testing.T) {
	target := &fakeTarget{}
	ts := newTestServer(t, target, nil)
	starts := target.count("start-end")

	ts.get(t, "/restart?seconds=1")
	code, _ := ts.get(t, "/stop")
	require.Equal(t, 200, code)

	time.Sleep(1500 * time.Millisecond)
	require.Equal(t, starts, target.count("start-end"), "the cancelled restart did not start: %v", target.log())

	code, _ = ts.get(t, "/start")
	require.Equal(t, 200, code)
	require.Equal(t, starts+1, target.count("start-end"))
}

func TestRestartAnswersAtOnceAndStartsAfterTheDelay(t *testing.T) {
	target := &fakeTarget{}
	ts := newTestServer(t, target, nil)
	starts := target.count("start-end")

	began := time.Now()
	code, body := ts.get(t, "/restart?seconds=1")

	require.Less(t, time.Since(began), 500*time.Millisecond)
	require.Equal(t, 200, code)
	require.Contains(t, body, "flagd will restart in 1 seconds")
	require.Equal(t, starts, target.count("start-end"), "not started yet")
	require.Eventually(t, func() bool { return target.count("start-end") == starts+1 }, 5*time.Second, 10*time.Millisecond)
	require.GreaterOrEqual(t, time.Since(began), time.Second)
}

func TestRestartWithoutSecondsUsesTheDefault(t *testing.T) {
	ts := newTestServer(t, &fakeTarget{}, func(c *Config) { c.DefaultRestartSeconds = 7 })

	_, body := ts.get(t, "/restart")

	require.Contains(t, body, "flagd will restart in 7 seconds")
}

// A later call cancels the pending restart: only the last restart starts.
func TestRestartIsCancelledByALaterRestart(t *testing.T) {
	target := &fakeTarget{}
	ts := newTestServer(t, target, nil)
	starts := target.count("start-end")

	ts.get(t, "/restart?seconds=1")
	ts.get(t, "/restart?seconds=2")

	time.Sleep(1500 * time.Millisecond)
	require.Equal(t, starts, target.count("start-end"), "the first restart was cancelled")
	require.Eventually(t, func() bool { return target.count("start-end") == starts+1 }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, starts+1, target.count("start-end"))
}

func TestRestartIsCancelledByAStart(t *testing.T) {
	target := &fakeTarget{}
	ts := newTestServer(t, target, nil)
	starts := target.count("start-end")

	ts.get(t, "/restart?seconds=1")
	ts.get(t, "/start")

	time.Sleep(1500 * time.Millisecond)
	require.Equal(t, starts+1, target.count("start-end"), "only the manual start ran: %v", target.log())
}

// A7: the target does not take the document in time.
func TestChangeTimesOut(t *testing.T) {
	target := &fakeTarget{loadFn: func(ctx context.Context, _ []byte, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	ts := newTestServer(t, target, func(c *Config) { c.ChangeTimeout = 100 * time.Millisecond })
	rendered := ts.rendered(t)
	document, version := ts.Document()

	code, contentType, body := ts.call(t, http.MethodGet, "/change")

	require.Equal(t, 500, code)
	require.Equal(t, "text/plain; charset=utf-8", contentType)
	require.Equal(t, "Failed to toggle changing flag: Flags were not updated in time: context deadline exceeded\n", body)
	require.Equal(t, rendered, ts.rendered(t), "nothing is rendered")
	gotDocument, gotVersion := ts.Document()
	require.NotEqual(t, document, gotDocument, "a timeout settles nothing, so the toggled document stays")
	require.NotEqual(t, version, gotVersion)

	// the target takes it in late; the next change toggles from the state that stayed
	target.setLoad(nil)
	code, body = ts.get(t, "/change")
	require.Equal(t, 200, code)
	require.Contains(t, body, "changed to 'foo'", "the toggle after the timeout starts from the toggled state")
	require.Equal(t, ts.rendered(t), func() []byte { d, _ := ts.Document(); return d }())
}

// R3-1: the target rejects the document, so nothing is rendered and the combined flags stay.
func TestLoadRejectedRendersNothing(t *testing.T) {
	target := &fakeTarget{loadFn: func(context.Context, []byte, string) error { return errors.New("flag definition rejected") }}
	ts := newTestServer(t, target, nil)
	rendered := ts.rendered(t)
	_, version := ts.Document()

	code, _, body := ts.call(t, http.MethodGet, "/change")

	require.Equal(t, 500, code)
	require.Equal(t, "Failed to toggle changing flag: flag definition rejected\n", body)
	require.Equal(t, rendered, ts.rendered(t))
	_, now := ts.Document()
	require.Equal(t, version, now)

	target.setLoad(nil)
	code, body = ts.get(t, "/change")
	require.Equal(t, 200, code)
	require.Contains(t, body, "changed to 'bar'", "the rejected toggle did not count")
}

// R3-1: the target reads the new document while it loads it, and gets its version.
func TestLoadGetsTheDocumentItCanRead(t *testing.T) {
	type seen struct {
		document, current []byte
		version, current_ string
	}
	var ts *testServer
	var got seen
	target := &fakeTarget{}
	target.setLoad(func(_ context.Context, document []byte, version string) error {
		current, currentVersion := ts.Document() // the target reads it while it loads
		got = seen{document: document, version: version, current: current, current_: currentVersion}
		return nil
	})
	ts = newTestServer(t, target, nil)

	ts.get(t, "/change")

	require.Equal(t, ts.rendered(t), got.document)
	require.Equal(t, got.document, got.current, "Document already returns what Load is handed")
	require.Equal(t, got.version, got.current_)
	require.Len(t, got.version, 64, "the digest of the document")
}

// R3-5: /change during an outage loads and renders, as the launchpad's file watcher did; the file is back while the
// ports are closed.
func TestChangeDuringOutageRenders(t *testing.T) {
	target := &fakeTarget{}
	ts := newTestServer(t, target, nil)

	ts.get(t, "/restart?seconds=30")
	_, err := os.Stat(ts.render)
	require.True(t, os.IsNotExist(err), "the file is gone during the outage")
	loads := target.count("load")

	code, _ := ts.get(t, "/change")

	require.Equal(t, 200, code)
	require.Equal(t, loads+1, target.count("load"))
	document, _ := ts.Document()
	require.Equal(t, document, ts.rendered(t), "rendered again while the target is closed")
}

// /change after /stop: the target is closed and the runtime runs behind it, so the document is loaded and rendered.
func TestChangeAfterStop(t *testing.T) {
	target := &fakeTarget{}
	ts := newTestServer(t, target, nil)
	ts.get(t, "/stop")
	loads := target.count("load")

	code, _ := ts.get(t, "/change")

	require.Equal(t, 200, code)
	require.Equal(t, loads+1, target.count("load"))
	document, _ := ts.Document()
	require.Equal(t, document, ts.rendered(t))
	require.Zero(t, target.count("start-begin")-1, "and nothing started again")
}

// R3-5: with no runtime to load into, /change fails at once and does not wait out its timeout.
func TestChangeWithNoRuntimeFailsAtOnce(t *testing.T) {
	target := &fakeTarget{loadFn: func(context.Context, []byte, string) error { return ErrNoRuntime }}
	ts := newTestServer(t, target, func(c *Config) { c.ChangeTimeout = 30 * time.Second })
	rendered := ts.rendered(t)

	began := time.Now()
	code, _, body := ts.call(t, http.MethodGet, "/change")

	require.Less(t, time.Since(began), time.Second)
	require.Equal(t, 500, code)
	require.Equal(t, "Failed to toggle changing flag: no runtime\n", body)
	require.Equal(t, rendered, ts.rendered(t))
}

func TestChangeWithoutTheChangingFlagFails(t *testing.T) {
	ts := newTestServer(t, &fakeTarget{}, func(c *Config) { c.ChangingFlag = "no-such-flag" })

	code, _, body := ts.call(t, http.MethodGet, "/change")

	require.Equal(t, 500, code)
	require.Equal(t, "Failed to toggle changing flag: no-such-flag not found in configuration\n", body)
}

// R3-6: one lock for every call, so a Load never overlaps a Start.
func TestChangeDuringStartIsSerialized(t *testing.T) {
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce) // a failed assertion must not leave the start blocked
	target := &fakeTarget{}
	target.startFn = func(_ context.Context, config string) error {
		if config == "ssl" {
			<-release
		}
		return nil
	}
	ts := newTestServer(t, target, nil)

	started := make(chan int, 1)
	go func() {
		code, _ := ts.get(t, "/start?config=ssl")
		started <- code
	}()
	require.Eventually(t, func() bool { return target.count("start-begin:ssl") == 1 }, 5*time.Second, 5*time.Millisecond)

	changed := make(chan int, 1)
	go func() {
		code, _ := ts.get(t, "/change")
		changed <- code
	}()
	time.Sleep(200 * time.Millisecond)
	require.Zero(t, target.count("load"), "the change waits for the start: %v", target.log())

	releaseOnce()
	require.Equal(t, 200, <-started)
	require.Equal(t, 200, <-changed)
	calls := target.log()
	require.Equal(t, []string{"start-begin:ssl", "start-end:ssl", "open", "load"}, calls[len(calls)-4:])
}

func TestChangeDuringTheDelayedStartIsSerialized(t *testing.T) {
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	target := &fakeTarget{}
	ts := newTestServer(t, target, nil)
	target.setStart(func(context.Context, string) error {
		<-release
		return nil
	})

	ts.get(t, "/restart?seconds=0")
	require.Eventually(t, func() bool { return target.count("start-begin") == 2 }, 5*time.Second, 5*time.Millisecond)

	changed := make(chan int, 1)
	go func() {
		code, _ := ts.get(t, "/change")
		changed <- code
	}()
	time.Sleep(200 * time.Millisecond)
	require.Zero(t, target.count("load"))

	releaseOnce()
	require.Equal(t, 200, <-changed)
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}.WithDefaults()

	require.Equal(t, ":8080", cfg.Address)
	require.Equal(t, "/configs", cfg.Configs)
	require.Equal(t, "/rawflags", cfg.RawFlags)
	require.Equal(t, "/flags/allFlags.json", cfg.Render)
	require.Equal(t, "default", cfg.DefaultConfig)
	require.Equal(t, "changing-flag", cfg.ChangingFlag)
	require.Equal(t, [2]string{"foo", "bar"}, cfg.ChangingVariants)
	require.Equal(t, 10*time.Second, cfg.StartTimeout)
	require.Equal(t, 5*time.Second, cfg.ChangeTimeout)
	require.Equal(t, 5, cfg.DefaultRestartSeconds)
	require.Empty(t, Config{NoRender: true}.WithDefaults().Render, "rendering can be turned off")
}
