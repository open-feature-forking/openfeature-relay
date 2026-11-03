package ofrep

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature/flagd/flagd/pkg/evaluation"
	compressmw "github.com/open-feature/flagd/flagd/pkg/service/middleware/compress"
	"github.com/stretchr/testify/require"
)

// newBenchmarkServer serves the OFREP endpoint over a real evaluator with today's noop metrics recorder
// and the noop telemetry provider.
func newBenchmarkServer(b *testing.B) *httptest.Server {
	b.Helper()

	log := logger.NewLogger(nil, false)
	flagStore, err := store.NewStore(log, []string{spanTestSource})
	require.NoError(b, err)
	eval := evaluation.NewEvaluator(log, flagStore)
	require.NoError(b, eval.SetState(coresync.DataSync{FlagData: spanTestFlags, Source: spanTestSource}))

	compression, err := compressmw.New()
	require.NoError(b, err)
	server := httptest.NewServer(NewOfrepHandler(log, eval, nil, nil, nil, "flagd", SSEConfig{}, compression))
	b.Cleanup(server.Close)
	return server
}

func benchmarkPost(b *testing.B, path string) {
	server := newBenchmarkServer(b)
	b.ReportAllocs()
	for b.Loop() {
		resp, err := http.Post(server.URL+path, "application/json", strings.NewReader("{}")) //nolint:noctx // benchmark
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("status %d", resp.StatusCode)
		}
	}
}

func BenchmarkOFREPSingle(b *testing.B) { benchmarkPost(b, "/ofrep/v1/evaluate/flags/bool-flag") }

func BenchmarkOFREPBulk(b *testing.B) { benchmarkPost(b, "/ofrep/v1/evaluate/flags") }
