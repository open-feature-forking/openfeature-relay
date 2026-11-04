package control

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeTarget is a Target that records its calls and does what its hooks say. Without a hook every call succeeds.
type fakeTarget struct {
	mu    sync.Mutex
	calls []string

	startFn func(ctx context.Context, config string) error
	stopFn  func(ctx context.Context) error
	loadFn  func(ctx context.Context, document []byte, version string) error
	openFn  func(ctx context.Context) error

	notReady atomic.Bool
}

func (f *fakeTarget) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

// log returns the calls so far.
func (f *fakeTarget) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// count returns how many calls start with the prefix.
func (f *fakeTarget) count(prefix string) int {
	n := 0
	for _, call := range f.log() {
		if strings.HasPrefix(call, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeTarget) hooks() (start func(context.Context, string) error, stop func(context.Context) error, load func(context.Context, []byte, string) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startFn, f.stopFn, f.loadFn
}

func (f *fakeTarget) setOpen(fn func(context.Context) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openFn = fn
}

func (f *fakeTarget) Open(ctx context.Context) error {
	f.record("open")
	f.mu.Lock()
	open := f.openFn
	f.mu.Unlock()
	if open != nil {
		return open(ctx)
	}
	return nil
}

func (f *fakeTarget) setStart(fn func(context.Context, string) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startFn = fn
}

func (f *fakeTarget) setLoad(fn func(context.Context, []byte, string) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadFn = fn
}

func (f *fakeTarget) Start(ctx context.Context, config string) error {
	f.record("start-begin:" + config)
	start, _, _ := f.hooks()
	var err error
	if start != nil {
		err = start(ctx, config)
	}
	f.record("start-end:" + config)
	return err
}

func (f *fakeTarget) Stop(ctx context.Context) error {
	f.record("stop")
	if _, stop, _ := f.hooks(); stop != nil {
		return stop(ctx)
	}
	return nil
}

func (f *fakeTarget) Load(ctx context.Context, document []byte, version string) error {
	f.record("load")
	if _, _, load := f.hooks(); load != nil {
		return load(ctx, document, version)
	}
	return nil
}

func (f *fakeTarget) Ready() bool { return !f.notReady.Load() }

// testServer is a server on a fake target, started with its default configuration, behind an httptest server.
type testServer struct {
	*Server
	target *fakeTarget
	http   *httptest.Server
	render string
}

// newTestServer combines the raw flags of testdata and starts the default configuration, as Serve does. The configs
// directory holds default, ssl and broken. mutate may change the configuration first.
func newTestServer(t *testing.T, target *fakeTarget, mutate func(*Config)) *testServer {
	t.Helper()
	dir := t.TempDir()
	configs := filepath.Join(dir, "configs")
	require.NoError(t, os.MkdirAll(configs, 0o755))
	for _, name := range []string{"default", "ssl", "broken"} {
		require.NoError(t, os.WriteFile(filepath.Join(configs, name+".json"), []byte("{}"), 0o644))
	}
	cfg := Config{
		Configs:  configs,
		RawFlags: filepath.Join("testdata", "rawflags"),
		Render:   filepath.Join(dir, "flags", "allFlags.json"),
	}
	if mutate != nil {
		mutate(&cfg)
	}

	s := New(cfg, target)
	require.NoError(t, s.load())
	s.mu.Lock()
	err := s.start(context.Background(), s.cfg.DefaultConfig)
	s.mu.Unlock()
	require.NoError(t, err)

	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		s.cancelRestart()
		ts.Close()
	})
	return &testServer{Server: s, target: target, http: ts, render: s.cfg.Render}
}

// call sends the request and returns the status, the content type and the body.
func (ts *testServer) call(t *testing.T, method, path string) (code int, contentType, body string) {
	t.Helper()
	req, err := http.NewRequest(method, ts.http.URL+path, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(data)
}

func (ts *testServer) get(t *testing.T, path string) (int, string) {
	t.Helper()
	code, _, body := ts.call(t, http.MethodGet, path)
	return code, body
}

// config returns the configuration in use.
func (ts *testServer) config() string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.current
}

func (ts *testServer) rendered(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(ts.render)
	require.NoError(t, err)
	return data
}
