package upgrade

import "testing"

// TestTransientMatchesOnlyNetworkFailures pins which failed builds buildWithRetry repeats.
func TestTransientMatchesOnlyNetworkFailures(t *testing.T) {
	for output, want := range map[string]bool{
		`go: k8s.io/apiserver@v0.32.0: read "https://proxy.golang.org/k8s.io/apiserver/@v/v0.32.0.mod": stream error: stream ID 1721; INTERNAL_ERROR; received from peer`: true,
		`go: example.test/m@v1.0.0: reading https://proxy.golang.org/example.test/m/@v/v1.0.0.mod: 502 Bad Gateway`:                                                       true,
		`dial tcp 142.250.0.1:443: i/o timeout`: true,
		`net/http: TLS handshake timeout`:       true,
		`read: connection reset by peer`:        true,
		`go: example.test/m@v1.0.0: reading https://proxy.golang.org/example.test/m/@v/v1.0.0.mod: 404 Not Found`:                   false,
		`./main.go:3:2: undefined: comp.NewFactory`:                                                                                 false,
		`go: cannot load module eventsink/webhook listed in go.work file: open eventsink/webhook/go.mod: no such file or directory`: false,
	} {
		if got := transient.MatchString(output); got != want {
			t.Errorf("transient(%q) = %t, want %t", output, got, want)
		}
	}
}
