// Package porttest gives tests their ports without the gap between choosing a port and using it. It has no
// dependencies, so a test module takes it with a replace directive; no module a distribution is built from may.
//
// A test that runs the runtime in its own process reserves each port with Reserve and gives the runtime Listen as
// Components.Listen: the runtime then binds the listener that is already open, and nothing can take the port in
// between. A test that starts a process takes its ports from Free, which never returns a port twice.
package porttest

import (
	"net"
	"strconv"
	"sync"
	"testing"
)

var (
	mu     sync.Mutex
	held   = map[int]net.Listener{}
	issued = map[int]bool{}
)

// listenLoopback opens a listener on a free loopback port that no call has returned before. The kernel may hand out a
// port again as soon as it is closed, so a port seen before keeps its listener open until a new one comes.
func listenLoopback(t testing.TB) net.Listener {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	var rejected []net.Listener
	defer func() {
		for _, l := range rejected {
			_ = l.Close()
		}
	}()
	for range 10000 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserving a free port: %v", err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		if issued[port] {
			rejected = append(rejected, l)
			continue
		}
		issued[port] = true
		return l
	}
	t.Fatal("no free port that was not handed out before")
	return nil
}

// Free returns a free port that no call of Free or Reserve has returned before, for a process the test starts. The
// port is closed when Free returns, so it is free to the process.
func Free(t testing.TB) int {
	t.Helper()
	l := listenLoopback(t)
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// Reserve returns a free port, as Free does, and keeps its listener open until Listen hands it over, Release closes it
// or the test ends.
func Reserve(t testing.TB) int {
	t.Helper()
	l := listenLoopback(t)
	port := l.Addr().(*net.TCPAddr).Port
	mu.Lock()
	held[port] = l
	mu.Unlock()
	t.Cleanup(func() { Release(port) })
	return port
}

// Release closes the listener of a reserved port that nothing took, so a client of it is refused as it is when the
// port serves nothing. It does nothing for a port that Listen has handed over.
func Release(ports ...int) {
	mu.Lock()
	defer mu.Unlock()
	for _, port := range ports {
		if l, ok := held[port]; ok {
			delete(held, port)
			_ = l.Close()
		}
	}
}

// Listen is a service.ListenFunc, for Components.Listen: it returns the open listener of the reserved port in address,
// and opens a new one with net.Listen for any other, including a reserved port that was handed over and closed.
func Listen(network, address string) (net.Listener, error) {
	if _, p, err := net.SplitHostPort(address); err == nil {
		if port, err := strconv.Atoi(p); err == nil {
			mu.Lock()
			l, ok := held[port]
			delete(held, port)
			mu.Unlock()
			if ok {
				return l, nil
			}
		}
	}
	return net.Listen(network, address)
}
