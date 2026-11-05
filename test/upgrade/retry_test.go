package upgrade

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestTransientMatchesOnlyNetworkFailures pins which failed builds buildWithRetry repeats.
func TestTransientMatchesOnlyNetworkFailures(t *testing.T) {
	for output, want := range map[string]bool{
		`go: k8s.io/apiserver@v0.32.0: read "https://proxy.golang.org/k8s.io/apiserver/@v/v0.32.0.mod": stream error: stream ID 1721; INTERNAL_ERROR; received from peer`: true,
		`go: example.test/m@v1.0.0: reading https://proxy.golang.org/example.test/m/@v/v1.0.0.mod: 502 Bad Gateway`:                                                       true,
		`dial tcp 142.250.0.1:443: i/o timeout`:                                                                                     true,
		`net/http: TLS handshake timeout`:                                                                                           true,
		`read: connection reset by peer`:                                                                                            true,
		`ERROR: failed to solve: error reading from server: EOF`:                                                                    true,
		`dial tcp: lookup registry-1.docker.io on 127.0.0.53:53: no such host`:                                                      true,
		`go: example.test/m@v1.0.0: reading https://proxy.golang.org/example.test/m/@v/v1.0.0.mod: 404 Not Found`:                   false,
		`./main.go:3:2: undefined: comp.NewFactory`:                                                                                 false,
		`go: cannot load module eventsink/webhook listed in go.work file: open eventsink/webhook/go.mod: no such file or directory`: false,
	} {
		if got := transient.MatchString(output); got != want {
			t.Errorf("transient(%q) = %t, want %t", output, got, want)
		}
	}
}

func TestTailKeepsTheEndOfTheBuildLog(t *testing.T) {
	if got := tail("a\nb", 3); got != "a\nb" {
		t.Errorf("a short log is kept whole, got %q", got)
	}
	if got, want := tail("1\n2\n3\n4\n5", 2), "[3 earlier lines of the build log left out]\n4\n5"; got != want {
		t.Errorf("tail = %q, want %q", got, want)
	}
}

// TestTransientIsTheSameEverywhere pins that the integration test and relay-builder repeat the same failures as this
// harness. The three live in separate modules, so each has its copy of the pattern; this test is what keeps them one.
func TestTransientIsTheSameEverywhere(t *testing.T) {
	declaration := regexp.MustCompile("(?m)^var transient = regexp\\.MustCompile\\(`([^`]*)`\\)")
	for _, file := range []string{
		filepath.Join("..", "integration", "integration_test.go"),
		filepath.Join("..", "..", "builder", "internal", "gotool", "gotool.go"),
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		m := declaration.FindSubmatch(data)
		if m == nil {
			t.Fatalf("%s declares no transient pattern", file)
		}
		if got := string(m[1]); got != transient.String() {
			t.Errorf("%s's transient pattern differs:\n got %s\nwant %s", file, got, transient.String())
		}
	}
}
