// Package gate puts a switch in front of the listeners of a runtime. Close drops the sockets the way a killed process
// does: the listeners close, so new connections are refused, and every accepted connection gets a plain Close, so open
// connections and streams end with a FIN and nothing is said at the application level (no Server.Shutdown, no GOAWAY).
// Open binds the same addresses again. The runtime keeps running behind a closed gate: its Accept calls block.
//
// Listen has the signature of net.Listen, so a gate's Listen fits wherever a runtime opens a listener. A listener
// stays registered until the runtime closes it, which is what lets a second runtime register on the same addresses
// after the first one has gone.
package gate

import (
	"fmt"
	"net"
	"sync"
)

// Gate is open when it is created.
type Gate struct {
	mu        sync.Mutex
	open      bool
	changed   chan struct{} // closed and replaced on every change that an Accept has to notice
	listeners map[*listener]struct{}
	conns     map[*conn]struct{}
}

func New() *Gate {
	return &Gate{
		open:      true,
		changed:   make(chan struct{}),
		listeners: map[*listener]struct{}{},
		conns:     map[*conn]struct{}{},
	}
}

// Listen registers a listener on network and address (tcp, tcp4, tcp6 or unix) and returns it. An address with port 0
// keeps the port it first got, also after the gate was closed and opened again. It fails if the address is already
// registered by a listener that has not been closed.
func (g *Gate) Listen(network, address string) (net.Listener, error) {
	g.mu.Lock()
	for other := range g.listeners {
		if other.addr.Network() == network && other.addr.String() == address {
			g.mu.Unlock()
			return nil, fmt.Errorf("gate: %s %s is already registered", network, address)
		}
	}
	g.mu.Unlock()
	nl, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	addr := nl.Addr()
	for other := range g.listeners {
		if other.addr.Network() == addr.Network() && other.addr.String() == addr.String() {
			_ = nl.Close()
			return nil, fmt.Errorf("gate: %s %s is already registered", addr.Network(), addr)
		}
	}
	l := &listener{gate: g, addr: addr}
	if g.open {
		l.os = nl
	} else {
		_ = nl.Close() // the address is only resolved: a closed gate refuses connections
	}
	g.listeners[l] = struct{}{}
	return l, nil
}

// Open binds every registered listener again. It binds all or none: when one address cannot be bound, the gate stays
// closed and the error says which.
func (g *Gate) Open() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.open {
		return nil
	}
	bound := map[*listener]net.Listener{}
	for l := range g.listeners {
		nl, err := net.Listen(l.addr.Network(), l.addr.String())
		if err != nil {
			for _, b := range bound {
				_ = b.Close()
			}
			return fmt.Errorf("gate: open %s %s: %w", l.addr.Network(), l.addr, err)
		}
		bound[l] = nl
	}
	for l, nl := range bound {
		l.os = nl
	}
	g.open = true
	g.signal()
	return nil
}

// Close closes the sockets. It does nothing when the gate is closed.
func (g *Gate) Close() {
	g.mu.Lock()
	if !g.open {
		g.mu.Unlock()
		return
	}
	g.open = false
	var sockets []net.Listener
	for l := range g.listeners {
		if l.os != nil {
			sockets = append(sockets, l.os)
			l.os = nil
		}
	}
	conns := make([]*conn, 0, len(g.conns))
	for c := range g.conns {
		conns = append(conns, c)
	}
	g.conns = map[*conn]struct{}{}
	g.signal()
	g.mu.Unlock()

	for _, s := range sockets {
		_ = s.Close()
	}
	for _, c := range conns {
		_ = c.Conn.Close()
	}
}

// IsOpen reports whether the gate is open.
func (g *Gate) IsOpen() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.open
}

func (g *Gate) signal() {
	close(g.changed)
	g.changed = make(chan struct{})
}

type listener struct {
	gate   *Gate
	addr   net.Addr
	os     net.Listener // nil while the gate is closed or the listener is closed; guarded by gate.mu
	closed bool         // guarded by gate.mu
}

// Accept blocks while the gate is closed. A connection accepted from a socket the gate closed in the meantime is
// dropped.
func (l *listener) Accept() (net.Conn, error) {
	g := l.gate
	for {
		g.mu.Lock()
		if l.closed {
			g.mu.Unlock()
			return nil, net.ErrClosed
		}
		sock, changed := l.os, g.changed
		g.mu.Unlock()
		if sock == nil {
			<-changed
			continue
		}

		c, err := sock.Accept()

		g.mu.Lock()
		switch {
		case l.closed:
			g.mu.Unlock()
			if c != nil {
				_ = c.Close()
			}
			return nil, net.ErrClosed
		case l.os != sock: // the gate was closed, and perhaps opened again, while this Accept waited
			g.mu.Unlock()
			if c != nil {
				_ = c.Close()
			}
			continue
		case err != nil:
			g.mu.Unlock()
			return nil, err
		}
		tc := &conn{Conn: c, gate: g}
		g.conns[tc] = struct{}{}
		g.mu.Unlock()
		return tc, nil
	}
}

// Close unregisters the listener and frees its address for another registration. Connections it accepted stay open.
func (l *listener) Close() error {
	g := l.gate
	g.mu.Lock()
	if l.closed {
		g.mu.Unlock()
		return net.ErrClosed
	}
	l.closed = true
	delete(g.listeners, l)
	sock := l.os
	l.os = nil
	g.signal()
	g.mu.Unlock()
	if sock == nil {
		return nil
	}
	return sock.Close()
}

func (l *listener) Addr() net.Addr { return l.addr }

type conn struct {
	net.Conn
	gate *Gate
}

func (c *conn) Close() error {
	c.gate.mu.Lock()
	delete(c.gate.conns, c)
	c.gate.mu.Unlock()
	return c.Conn.Close()
}
