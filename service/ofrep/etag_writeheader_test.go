package ofrep

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConditionalETag_FirstWriteHeaderWins: a handler that writes a status twice is answered with the first, as
// net/http answers it, and the 304 for a matching If-None-Match still follows from it.
func TestConditionalETag_FirstWriteHeaderWins(t *testing.T) {
	const body = `{"flags":[]}`
	handler := conditionalETag(noConfigEtagVeto, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.WriteHeader(http.StatusInternalServerError) // ignored
		_, _ = w.Write([]byte(body))
	}))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags", nil))
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, body, first.Body.String())
	etag := first.Header().Get("ETag")
	require.Equal(t, bodyETag([]byte(body)), etag)

	req := httptest.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags", nil)
	req.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)
	require.Equal(t, http.StatusNotModified, second.Code)
	require.Empty(t, second.Body.String())
	require.Equal(t, etag, second.Header().Get("ETag"))
}

// TestConditionalETag_ErrorStatusIsNotOverwritten: the first status stays when a later one would have been a 200.
func TestConditionalETag_ErrorStatusIsNotOverwritten(t *testing.T) {
	handler := conditionalETag(noConfigEtagVeto, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.WriteHeader(http.StatusOK) // ignored
		_, _ = w.Write([]byte(`{}`))
	}))
	req := httptest.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags", nil)
	req.Header.Set("If-None-Match", "*")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, rec.Header().Get("ETag"))
}
