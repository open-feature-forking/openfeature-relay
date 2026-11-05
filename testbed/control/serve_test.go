package control

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Serve combines the flags, listens on its own address, starts the default configuration, renders the file, and on
// shutdown removes it again.
func TestServe(t *testing.T) {
	dir := t.TempDir()
	configs := filepath.Join(dir, "configs")
	require.NoError(t, os.MkdirAll(configs, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configs, "default.json"), []byte("{}"), 0o644))
	render := filepath.Join(dir, "flags", "allFlags.json")
	target := &fakeTarget{}
	s := New(Config{Address: "127.0.0.1:0", Configs: configs, RawFlags: filepath.Join("testdata", "rawflags"), Render: render}, target)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx) }()

	require.Eventually(t, func() bool { return s.Addr() != "" }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, 1, target.count("start-end:default"), "the default configuration started before the API listened")
	resp, err := http.Get("http://" + s.Addr() + "/change")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode, string(body))
	data, err := os.ReadFile(render)
	require.NoError(t, err)
	require.Equal(t, golden(t, "change1"), data)

	cancel()
	require.NoError(t, <-served)
	_, err = os.Stat(render)
	require.True(t, os.IsNotExist(err), "the rendered file is removed at shutdown, as the launchpad does")
	_, err = net.DialTimeout("tcp", s.Addr(), 200*time.Millisecond)
	require.Error(t, err, "the API is closed")
}

func TestServeStopsWhenTheDefaultConfigurationDoesNotStart(t *testing.T) {
	configs := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(configs, "default.json"), []byte("{}"), 0o644))
	address := freeAddress(t)
	target := &fakeTarget{startFn: func(context.Context, string) error { return context.DeadlineExceeded }}
	s := New(Config{Address: address, Configs: configs, RawFlags: filepath.Join("testdata", "rawflags"), Render: filepath.Join(t.TempDir(), "a.json")}, target)

	err := s.Serve(t.Context())

	require.ErrorContains(t, err, "failed to start flagd")
	require.Empty(t, s.Addr(), "the API never listened")
	_, dialErr := net.DialTimeout("tcp", address, 200*time.Millisecond)
	require.Error(t, dialErr, "the port refuses")
	require.Equal(t, 1, target.count("stop"), "the target that did not get ready is closed")
}

// freeAddress returns a loopback address that nothing listens on.
func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := l.Addr().String()
	require.NoError(t, l.Close())
	return address
}

// The port refuses connections until the default configuration is ready (UC-022 step 2), and the file is rendered
// before the target is opened, for the first start as for every other.
func TestServeOpensThePortWhenTheDefaultConfigurationIsReady(t *testing.T) {
	configs := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(configs, "default.json"), []byte("{}"), 0o644))
	address := freeAddress(t)
	render := filepath.Join(t.TempDir(), "flags", "allFlags.json")
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	renderedAtOpen := make(chan []byte, 1)
	target := &fakeTarget{}
	target.setStart(func(context.Context, string) error {
		<-release // the target becomes ready after a delay
		return nil
	})
	target.setOpen(func(context.Context) error {
		data, _ := os.ReadFile(render)
		renderedAtOpen <- data
		return nil
	})
	s := New(Config{Address: address, Configs: configs, RawFlags: filepath.Join("testdata", "rawflags"), Render: render}, target)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx) }()

	require.Eventually(t, func() bool { return target.count("start-begin:default") == 1 }, 5*time.Second, 5*time.Millisecond)
	for range 20 {
		_, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		require.Error(t, err, "the port refuses while the default configuration starts")
		time.Sleep(10 * time.Millisecond)
	}
	require.Empty(t, s.Addr())

	releaseOnce()
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}, 5*time.Second, 10*time.Millisecond, "the port opens once the target is ready")
	require.Equal(t, golden(t, "start"), <-renderedAtOpen, "rendered before the target was opened")

	cancel()
	require.NoError(t, <-served)
}

func TestServeFailsOnMissingRawFlags(t *testing.T) {
	s := New(Config{Address: "127.0.0.1:0", RawFlags: filepath.Join(t.TempDir(), "missing")}, &fakeTarget{})

	err := s.Serve(t.Context())

	require.ErrorContains(t, err, "error during initial JSON combination")
}

func TestServeFailsWhenTheAddressIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer taken.Close()
	s := New(Config{Address: taken.Addr().String(), RawFlags: filepath.Join("testdata", "rawflags")}, &fakeTarget{})

	require.Error(t, s.Serve(t.Context()))
}
