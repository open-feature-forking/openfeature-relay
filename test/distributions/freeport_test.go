package distributions

import (
	"testing"
)

// TestFreePortNeverRepeats: the kernel hands a closed port out again, and a start given one port for two of its flags
// fails to bind (#175).
func TestFreePortNeverRepeats(t *testing.T) {
	var ports portSet
	seen := map[int]bool{}
	for range 2000 {
		port := ports.free(t)
		if seen[port] {
			t.Fatalf("port %d was handed out twice", port)
		}
		seen[port] = true
	}
}
