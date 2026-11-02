package sync

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
)

func BenchmarkServeFlags(b *testing.B) {
	flagStore, _ := getSimpleFlagStore(b)
	mt := &modTime{}
	mt.set(time.Now())

	mux := http.NewServeMux()
	mux.Handle("GET "+flagsPath, httpHandler{
		store:   flagStore,
		log:     logger.NewLogger(nil, false),
		modTime: mt,
	})
	req := httptest.NewRequest(http.MethodGet, flagsPath, nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mux.ServeHTTP(httptest.NewRecorder(), req)
	}
}
