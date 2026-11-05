//go:build unix

package runtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/open-feature-forking/openfeature-relay/runtime/internal/officialset"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

// TestMiddlewareSeesH2CRequests pins that the middleware of a distribution answers a prior-knowledge HTTP/2 gRPC call
// without a key, on flag evaluation and flag sync, with and without TLS: h2c is a layer outside the chain, so the
// middleware sees the call as an HTTP/2 request.
func TestMiddlewareSeesH2CRequests(t *testing.T) {
	ca, err := os.ReadFile(certDir + "ca-cert.pem")
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(ca))

	for _, useTLS := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%t", useTLS), func(t *testing.T) {
			var seen atomic.Int64
			p := newPorts(t)
			cfg := characterizedConfig(t, p)
			if useTLS {
				cfg.ServiceCertPath, cfg.ServiceKeyPath = certDir+"server-cert.pem", certDir+"server-key.pem"
			}
			components := officialset.Components()
			components.Middleware = []middleware.Configured{{Factory: keyRequiredFactory{seen: &seen}}}
			startRuntimeWith(t, cfg, components)

			transport := &http2.Transport{}
			scheme, host := "http", "127.0.0.1"
			if useTLS {
				scheme, host = "https", "0.0.0.0" // the name the test certificate is valid for
				transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
			} else {
				transport.AllowHTTP = true
				transport.DialTLSContext = func(ctx context.Context, _, addr string, _ *tls.Config) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
				}
			}
			client := &http.Client{Transport: transport, Timeout: 10 * time.Second}

			for _, endpoint := range []struct {
				name, path string
				port       uint16
			}{
				{"flag evaluation", "/flagd.evaluation.v1.Service/ResolveBoolean", p.service},
				{"flag sync", "/flagd.sync.v1.FlagSyncService/FetchAllFlags", p.sync},
			} {
				t.Run(endpoint.name, func(t *testing.T) {
					call := func(key string) *http.Response {
						req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s://%s:%d%s", scheme, host, endpoint.port, endpoint.path),
							bytes.NewReader([]byte{0, 0, 0, 0, 0}))
						require.NoError(t, err)
						req.Header.Set("Content-Type", "application/grpc")
						if key != "" {
							req.Header.Set("X-Key", key)
						}
						resp, err := client.Do(req)
						require.NoError(t, err)
						resp.Body.Close()
						return resp
					}

					before := seen.Load()
					resp := call("")
					require.Equal(t, 2, resp.ProtoMajor)
					require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "the middleware answers the call without a key")
					require.Equal(t, before+1, seen.Load())

					resp = call("secret")
					require.Equal(t, 2, resp.ProtoMajor)
					require.Equal(t, http.StatusOK, resp.StatusCode, "a call with a key passes to the endpoint")
				})
			}
		})
	}
}
