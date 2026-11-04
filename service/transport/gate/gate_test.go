package gate_test

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/service/transport/gate"
	"github.com/stretchr/testify/require"
)

type target struct{ network, address string }

func targets(t *testing.T) map[string]target {
	t.Helper()
	m := map[string]target{"tcp": {"tcp", "127.0.0.1:0"}}
	if runtime.GOOS != "windows" {
		m["unix"] = target{"unix", filepath.Join(t.TempDir(), "gate.sock")}
	}
	return m
}

// echo accepts connections on l and echoes what they send until l is closed.
func echo(l net.Listener) {
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
}

func roundTrip(t *testing.T, addr net.Addr) {
	t.Helper()
	c, err := net.DialTimeout(addr.Network(), addr.String(), 2*time.Second)
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Write([]byte("ping"))
	require.NoError(t, err)
	buf := make([]byte, 4)
	_, err = io.ReadFull(c, buf)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf))
}

func TestCloseRefusesAndOpenServesAgain(t *testing.T) {
	for name, tg := range targets(t) {
		t.Run(name, func(t *testing.T) {
			g := gate.New()
			l, err := g.Listen(tg.network, tg.address)
			require.NoError(t, err)
			defer l.Close()
			echo(l)
			addr := l.Addr()
			roundTrip(t, addr)

			g.Close()
			require.False(t, g.IsOpen())
			_, err = net.DialTimeout(addr.Network(), addr.String(), time.Second)
			require.Error(t, err, "a closed gate refuses connections")

			require.NoError(t, g.Open())
			require.Equal(t, addr.String(), l.Addr().String(), "the address survives a close and an open")
			roundTrip(t, addr)
		})
	}
}

func TestCloseBreaksOpenStreamsWithAPlainClose(t *testing.T) {
	for name, tg := range targets(t) {
		t.Run(name, func(t *testing.T) {
			g := gate.New()
			l, err := g.Listen(tg.network, tg.address)
			require.NoError(t, err)
			defer l.Close()
			echo(l)

			c, err := net.Dial(l.Addr().Network(), l.Addr().String())
			require.NoError(t, err)
			defer c.Close()
			_, err = c.Write([]byte("x"))
			require.NoError(t, err)
			_, err = io.ReadFull(c, make([]byte, 1))
			require.NoError(t, err)

			g.Close()
			require.NoError(t, c.SetReadDeadline(time.Now().Add(2*time.Second)))
			_, err = c.Read(make([]byte, 1))
			require.ErrorIs(t, err, io.EOF, "an idle stream ends with a FIN, not a reset")
		})
	}
}

func TestAcceptBlocksWhileClosedAndAServerResumes(t *testing.T) {
	g := gate.New()
	l, err := g.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })}
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(l) }()
	defer func() { _ = srv.Close(); <-done }()
	url := "http://" + l.Addr().String()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 2 * time.Second}

	get := func() error {
		resp, err := client.Get(url)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, err = io.ReadAll(resp.Body)
		return err
	}
	require.NoError(t, get())
	g.Close()
	require.Error(t, get())
	time.Sleep(50 * time.Millisecond) // the server's Accept is blocked, not failed
	require.NoError(t, g.Open())
	require.NoError(t, get(), "the same server serves again after Open")
}

func TestListenWhileClosedKeepsTheAddress(t *testing.T) {
	g := gate.New()
	g.Close()
	l, err := g.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	addr := l.Addr()
	_, err = net.DialTimeout("tcp", addr.String(), time.Second)
	require.Error(t, err)
	echo(l)
	require.NoError(t, g.Open())
	roundTrip(t, addr)
}

// A second runtime registers its listeners on the same gate and the same addresses after the first has gone, also
// when the gate was closed in between.
func TestSecondRuntimeRegistersAfterTheFirstHasGone(t *testing.T) {
	for name, tg := range targets(t) {
		t.Run(name, func(t *testing.T) {
			g := gate.New()
			first, err := g.Listen(tg.network, tg.address)
			require.NoError(t, err)
			echo(first)
			addr := first.Addr()

			_, err = g.Listen(addr.Network(), addr.String())
			require.Error(t, err, "while the first listener is registered, the address is taken")

			g.Close()
			require.NoError(t, first.Close(), "the first runtime goes away behind a closed gate")
			require.Error(t, first.Close(), "closing twice is an error, as for any listener")
			_, err = first.Accept()
			require.ErrorIs(t, err, net.ErrClosed)

			second, err := g.Listen(addr.Network(), addr.String())
			require.NoError(t, err)
			defer second.Close()
			echo(second)
			_, err = net.DialTimeout(addr.Network(), addr.String(), time.Second)
			require.Error(t, err, "the gate is still closed")
			require.NoError(t, g.Open())
			roundTrip(t, addr)
		})
	}
}

func TestOpenFailsWholeWhenAnAddressIsTaken(t *testing.T) {
	g := gate.New()
	l, err := g.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	g.Close()
	squatter, err := net.Listen("tcp", l.Addr().String())
	require.NoError(t, err)
	require.Error(t, g.Open())
	require.False(t, g.IsOpen())
	require.NoError(t, squatter.Close())
	require.NoError(t, g.Open(), "a later Open succeeds once the address is free")
}

func TestReopenCycles(t *testing.T) {
	g := gate.New()
	l, err := g.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	echo(l)
	addr := l.Addr()
	for i := 0; i < 100; i++ {
		g.Close()
		_, err := net.DialTimeout("tcp", addr.String(), time.Second)
		require.Error(t, err, "cycle %d", i)
		require.NoError(t, g.Open(), "cycle %d", i)
		roundTrip(t, addr)
	}
}

// Listen neither replaces a registered listener nor waits for it: it fails and names the address.
func TestListenOnRegisteredAddressFails(t *testing.T) {
	for name, tg := range targets(t) {
		for _, closed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/closed=%v", name, closed), func(t *testing.T) {
				g := gate.New()
				first, err := g.Listen(tg.network, tg.address)
				require.NoError(t, err)
				defer first.Close()
				echo(first)
				addr := first.Addr()
				if closed {
					g.Close()
				}

				start := time.Now()
				second, err := g.Listen(addr.Network(), addr.String())
				require.Error(t, err)
				require.Nil(t, second)
				require.Contains(t, err.Error(), addr.String())
				require.Less(t, time.Since(start), time.Second, "Listen does not wait")

				require.NoError(t, g.Open())
				roundTrip(t, addr) // the first listener is still the registered one
			})
		}
	}
}

func TestListenAfterReleaseSucceeds(t *testing.T) {
	for name, tg := range targets(t) {
		t.Run(name, func(t *testing.T) {
			g := gate.New()
			first, err := g.Listen(tg.network, tg.address)
			require.NoError(t, err)
			addr := first.Addr()
			require.NoError(t, first.Close())

			second, err := g.Listen(addr.Network(), addr.String())
			require.NoError(t, err, "a closed listener has released its address")
			defer second.Close()
			echo(second)
			roundTrip(t, addr)
		})
	}
}

// The runtime passes ":8013", which the registry holds as "[::]:8013". With the gate closed the OS does not reject
// the duplicate either, so only the check after the bind catches it.
func TestListenOnRegisteredWildcardAddressFailsWhileClosed(t *testing.T) {
	g := gate.New()
	first, err := g.Listen("tcp", ":0")
	require.NoError(t, err)
	defer first.Close()
	port := first.Addr().(*net.TCPAddr).Port
	g.Close()

	second, err := g.Listen("tcp", fmt.Sprintf(":%d", port))
	require.Error(t, err)
	require.Nil(t, second)
	require.Contains(t, err.Error(), "already registered")
}

// A killed process leaves its socket file: clients get ECONNREFUSED, not ENOENT.
func TestUnixOutageRefusesConnections(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix sockets")
	}
	g := gate.New()
	l, err := g.Listen("unix", filepath.Join(t.TempDir(), "gate.sock"))
	require.NoError(t, err)
	defer l.Close()
	echo(l)
	addr := l.Addr()

	g.Close()
	_, err = net.Dial("unix", addr.String())
	require.ErrorIs(t, err, syscall.ECONNREFUSED)
	require.False(t, errors.Is(err, syscall.ENOENT))

	require.NoError(t, g.Open(), "Open removes the stale file before it binds")
	roundTrip(t, addr)
}

func TestUnixListenWhileClosedRefusesConnections(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix sockets")
	}
	g := gate.New()
	g.Close()
	l, err := g.Listen("unix", filepath.Join(t.TempDir(), "gate.sock"))
	require.NoError(t, err)
	defer l.Close()
	_, err = net.Dial("unix", l.Addr().String())
	require.ErrorIs(t, err, syscall.ECONNREFUSED)
}
