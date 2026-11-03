//go:build unix

package runtime

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNoopWhenOmitted: a distribution that selects no telemetry provider serves its endpoints, and answers /metrics
// with 404, since the noop telemetry provider serves no metrics.
func TestNoopWhenOmitted(t *testing.T) {
	p := newPorts(t)
	startRuntime(t, characterizedConfig(t, p))

	resp, err := http.Post( //nolint:gosec,noctx // local test endpoint
		fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/evaluate/flags/f1", p.ofrep), "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	require.Contains(t, string(body), `"value":true`)

	metrics, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", p.management)) //nolint:gosec,noctx // local test endpoint
	require.NoError(t, err)
	require.NoError(t, metrics.Body.Close())
	require.Equal(t, http.StatusNotFound, metrics.StatusCode)
}
