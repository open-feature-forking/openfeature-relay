// Package porttest gives the runtime's tests their ports without a gap between choosing a port and binding it.
//
// Port opens a listener on a free port and keeps it open. The runtime opens its listeners through Components.Listen;
// Listen hands it the open listener for the port it asks for, so the port is never released between the choice and the
// bind and no other test or process can take it meanwhile. A port nobody asks for is closed with its test.
package porttest

import (
	"net"
	"strconv"
	"sync"
	"testing"
)

var (
	mu   sync.Mutex
	held = map[int]net.Listener{}
)

// Port opens a listener on a free loopback port, keeps it open until Listen hands it over or the test ends, and returns
// the port.
func Port(t testing.TB) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	mu.Lock()
	held[port] = l
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		l, ok := held[port]
		delete(held, port)
		mu.Unlock()
		if ok {
			_ = l.Close()
		}
	})
	return uint16(port)
}

// Release closes the listener of a port that nothing asked for, so a client of it is refused as it is when the port
// serves nothing. It does nothing for a port that is already handed over.
func Release(ports ...uint16) {
	mu.Lock()
	defer mu.Unlock()
	for _, port := range ports {
		if l, ok := held[int(port)]; ok {
			delete(held, int(port))
			_ = l.Close()
		}
	}
}

// Listen is a service.ListenFunc: it returns the open listener of the port in address if Port reserved it, and opens
// the listener with net.Listen otherwise (a port that was handed over and closed, or one no test reserved).
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
