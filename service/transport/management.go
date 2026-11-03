package service

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c" //nolint:staticcheck // deprecated package, still functionally correct
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// NewManagementServer returns the server of the management port: /healthz, /readyz, /metrics and the gRPC health
// service. ready decides /readyz; a nil metrics handler answers /metrics with 404.
func NewManagementServer(port uint16, ready func() bool, metrics http.Handler) *http.Server {
	srv := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(srv, health.NewServer())

	mux := http.NewServeMux()
	mux.Handle("/healthz", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mux.Handle("/readyz", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if ready() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusPreconditionFailed)
		}
	}))
	if metrics != nil {
		mux.Handle("/metrics", metrics)
	}

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// if this is 'application/grpc' and HTTP2, handle with gRPC, otherwise HTTP.
		if request.ProtoMajor == 2 && strings.HasPrefix(request.Header.Get("Content-Type"), "application/grpc") {
			srv.ServeHTTP(writer, request)
		} else {
			mux.ServeHTTP(writer, request)
		}
	})

	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		ReadHeaderTimeout: 3 * time.Second,
		// slowloris/slow-client DoS protection; safe for server streams
		ReadTimeout: 5 * time.Second,
		// h2c carries plaintext HTTP/2 for the gRPC health service
		//nolint:staticcheck // the deprecated package still works; setting Protocols on the server would need more refactoring
		Handler: h2c.NewHandler(handler, &http2.Server{}),
	}
}
