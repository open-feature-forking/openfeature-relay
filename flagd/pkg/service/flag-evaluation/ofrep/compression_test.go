package ofrep

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	mock "github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/mock"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/open-feature/flagd/flagd/internal/telemetrytest"
	compressmw "github.com/open-feature/flagd/flagd/pkg/service/middleware/compress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/mock/gomock"
)

// recordedResponseSizes returns what the HTTP server metrics report as the response size of each request, which is
// the figure operators read as bytes served.
func recordedResponseSizes(t *testing.T, tel *telemetrytest.Recorder) []int64 {
	t.Helper()

	data, err := tel.Collect(context.Background())
	require.NoError(t, err)
	var sizes []int64
	for _, sm := range data.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "http.server.response.body.size" {
				continue
			}
			for _, point := range m.Data.(metricdata.Histogram[float64]).DataPoints {
				require.Equal(t, uint64(1), point.Count, "one request per data point")
				sizes = append(sizes, int64(point.Sum))
			}
		}
	}
	return sizes
}

// evaluations builds a bulk evaluation result of n flags.
func evaluations(n int) []evaluator.AnyValue {
	values := make([]evaluator.AnyValue, 0, n)
	for i := range n {
		values = append(values, evaluator.AnyValue{
			Value:   true,
			Variant: "on",
			Reason:  model.StaticReason,
			FlagKey: fmt.Sprintf("flag-%d", i),
		})
	}

	return values
}

// compressingHandler builds the production route chain with compression enabled.
func compressingHandler(t *testing.T, tp telemetry.Provider, evaluations []evaluator.AnyValue) http.Handler {
	t.Helper()

	eval := mock.NewMockIEvaluator(gomock.NewController(t))
	eval.EXPECT().ResolveAllValues(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(evaluations, model.Metadata{}, nil).AnyTimes()

	compression, err := compressmw.New()
	require.NoError(t, err)

	return NewOfrepHandler(logger.NewLogger(nil, false), eval, nil, nil, tp, "flagd", SSEConfig{}, compression)
}

func serveCompressedBulk(handler http.Handler, acceptEncoding, ifNoneMatch string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags", nil)
	req.Header.Set("Accept-Encoding", acceptEncoding)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	return recorder
}

func TestBulkEvaluationIsCompressed(t *testing.T) {
	handler := compressingHandler(t, nil, evaluations(3))

	recorder := serveCompressedBulk(handler, "gzip", "")

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "gzip", recorder.Header().Get("Content-Encoding"))

	reader, err := gzip.NewReader(recorder.Body)
	require.NoError(t, err)
	decoded, err := io.ReadAll(reader)
	require.NoError(t, err)

	var body struct {
		Flags []map[string]any `json:"flags"`
	}
	require.NoError(t, json.Unmarshal(decoded, &body))
	assert.Len(t, body.Flags, 3)
}

// The validator is weak, so it covers the compressed and uncompressed forms of one body alike and
// a client revalidates successfully whether or not its Accept-Encoding has changed since.
func TestBulkEvaluationETagIsEncodingAgnostic(t *testing.T) {
	handler := compressingHandler(t, nil, evaluations(3))

	gzipETag := serveCompressedBulk(handler, "gzip", "").Header().Get("ETag")
	identityETag := serveCompressedBulk(handler, "identity", "").Header().Get("ETag")

	require.NotEmpty(t, identityETag)
	require.True(t, strings.HasPrefix(identityETag, `W/"`), "expected a weak validator, got %q", identityETag)
	require.Equal(t, identityETag, gzipETag, "one weak tag should cover both encodings")

	tests := map[string]string{
		"still accepting gzip": "gzip",
		"no longer accepting":  "identity",
	}

	for name, acceptEncoding := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := serveCompressedBulk(handler, acceptEncoding, gzipETag)
			assert.Equal(t, http.StatusNotModified, recorder.Code)
			assert.Equal(t, gzipETag, recorder.Header().Get("ETag"), "the 304 must carry the tag of what would be served")
		})
	}
}

// The metrics middleware sits outside compression, so the size it reports is what actually goes on
// the wire rather than the body the evaluator produced.
func TestRecordedResponseSizeIsTheCompressedSize(t *testing.T) {
	tel := telemetrytest.New()
	handler := compressingHandler(t, tel, evaluations(3))

	recorder := serveCompressedBulk(handler, "gzip", "")

	sizes := recordedResponseSizes(t, tel)
	require.Len(t, sizes, 1)
	assert.Equal(t, int64(recorder.Body.Len()), sizes[0])
}

// The SSE stream must stay uncompressed so each event reaches subscribers as it is flushed, which
// means compression has to be wired onto the evaluate routes rather than around the whole mux.
func TestSSEStreamIsNotCompressed(t *testing.T) {
	eval := mock.NewMockIEvaluator(gomock.NewController(t))
	flagStore, err := store.NewStore(logger.NewLogger(nil, false), []string{"src1"})
	require.NoError(t, err)

	service, err := NewOfrepService(eval, flagStore, []string{"*"}, SvcConfiguration{
		Logger:      logger.NewLogger(nil, false),
		Port:        18286,
		ServiceName: testServiceName,
		SSEEnabled:  true,
	}, nil, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, ssePath, nil).WithContext(ctx)
	req.Header.Set("Accept-Encoding", "gzip")

	recorder := httptest.NewRecorder()
	service.server.Handler.ServeHTTP(recorder, req)

	assert.Empty(t, recorder.Header().Get("Content-Encoding"))
}
