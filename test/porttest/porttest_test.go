package porttest

import (
	"net"
	"strconv"
	"testing"
)

// The kernel hands a closed port out again, and a start given one port for two of its flags fails to bind.
func TestPortsAreNeverHandedOutTwice(t *testing.T) {
	seen := map[int]bool{}
	for i := range 200 {
		var port int
		if i%2 == 0 {
			port = Free(t)
		} else {
			port = Reserve(t)
		}
		if seen[port] {
			t.Fatalf("port %d was handed out twice", port)
		}
		seen[port] = true
	}
}

func TestListenHandsOverTheReservedListener(t *testing.T) {
	port := Reserve(t)
	if _, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
		t.Fatal("a reserved port is in use until it is handed over")
	}
	l, err := Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	if got := l.Addr().(*net.TCPAddr).Port; got != port {
		t.Fatalf("handed over port %d, reserved %d", got, port)
	}
	_ = l.Close()
	again, err := Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("a closed port opens again: %v", err)
	}
	_ = again.Close()
}

func TestReleaseRefusesClients(t *testing.T) {
	port := Reserve(t)
	Release(port)
	if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
		_ = c.Close()
		t.Fatal("a released port accepts connections")
	}
}
