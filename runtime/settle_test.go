//go:build linux

package runtime

import (
	"fmt"
	"net/http"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// ignoreServing is goleak.IgnoreCurrent for a running runtime, taken once every endpoint on ports answers HTTP and the
// goroutine count has then stopped moving (ten equal samples, 20 ms apart, at most 5 s). Readiness follows the
// sources, not the endpoints: a ready runtime can still be starting the goroutines an endpoint serves with, and a
// snapshot taken then reports them as leaked later.
func ignoreServing(t *testing.T, ports ...uint16) goleak.Option {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: time.Second}
	for _, port := range ports {
		// any answer, a TLS endpoint's 400 to plain HTTP included, comes from the endpoint's Serve
		require.Eventually(t, func() bool {
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port)) //nolint:noctx // local test endpoint
			if err != nil {
				return false
			}
			_ = resp.Body.Close()
			return true
		}, 10*time.Second, 5*time.Millisecond, "the endpoint on port %d answers", port)
	}

	const stableFor = 10
	deadline := time.Now().Add(5 * time.Second)
	last, same := goruntime.NumGoroutine(), 0
	for same < stableFor && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		if n := goruntime.NumGoroutine(); n == last {
			same++
		} else {
			last, same = n, 0
		}
	}
	return goleak.IgnoreCurrent()
}
