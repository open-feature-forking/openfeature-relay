package telemetry

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	coretel "github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// httpMetrics records the HTTP server metrics of each request: request duration, response size and active
// requests, under the route the options name.
type httpMetrics struct {
	recorder *MetricsRecorder
	opts     coretel.HTTPMetricsOptions
}

func newHTTPMetrics(recorder *MetricsRecorder, opts coretel.HTTPMetricsOptions) httpMetrics {
	return httpMetrics{recorder: recorder, opts: opts}
}

func (m httpMetrics) handler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wi := &responseWriterInterceptor{
			statusCode:     http.StatusOK,
			ResponseWriter: w,
		}
		route := m.opts.Route
		if m.opts.RouteFromRequest != nil {
			route = m.opts.RouteFromRequest(r)
		}
		// an unset route falls back to the URL path
		if route == "" {
			route = r.URL.Path
		}
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}

		// in-flight start and end must share attributes
		inFlightAttrs := m.recorder.HTTPAttributes(m.opts.Service, route, r.Method, strconv.Itoa(wi.statusCode), scheme)
		ctx := r.Context()
		m.recorder.InFlightRequestStart(ctx, inFlightAttrs)
		defer m.recorder.InFlightRequestEnd(ctx, inFlightAttrs)

		start := time.Now()
		defer func() {
			duration := time.Since(start)
			// status code is only known after the handler has run
			attrs := m.recorder.HTTPAttributes(m.opts.Service, route, r.Method, strconv.Itoa(wi.statusCode), scheme)
			m.recorder.HTTPRequestDuration(ctx, duration, attrs)
			m.recorder.HTTPResponseSize(ctx, int64(wi.bytesWritten), attrs)
		}()

		h.ServeHTTP(wi, r)
	})
}

// responseWriterInterceptor is a simple wrapper to intercept set data on a
// ResponseWriter.
type responseWriterInterceptor struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (w *responseWriterInterceptor) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseWriterInterceptor) Write(p []byte) (int, error) {
	w.bytesWritten += len(p)
	i, err := w.ResponseWriter.Write(p)
	if err != nil {
		return 0, fmt.Errorf("unable to write response: %w", err)
	}
	return i, nil
}

func (w *responseWriterInterceptor) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("type assertion failed http.ResponseWriter not a http.Hijacker")
	}
	conn, buf, err := h.Hijack()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to hijack connection: %w", err)
	}
	return conn, buf, nil
}

// Flush need to exist to be compatible with connect-go.
// See https://github.com/connectrpc/connect-go/blob/main/protocol_connect.go
func (w *responseWriterInterceptor) Flush() {
	f, ok := w.ResponseWriter.(http.Flusher)
	if !ok {
		return
	}
	f.Flush()
}
