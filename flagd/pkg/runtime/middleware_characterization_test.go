//go:build unix

package runtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature/flagd/flagd/internal/telemetrytest"
	flageval "github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation"
	"github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation/ofrep"
	flagsync "github.com/open-feature/flagd/flagd/pkg/service/flag-sync"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"golang.org/x/net/http2"
)

// requestLimit is the request body limit of the oversized-body cases.
const requestLimit = 64

// recordedStatuses returns how many requests the HTTP server metrics recorded for each response status code.
func recordedStatuses(t *testing.T, reader metric.Reader) map[string]uint64 {
	t.Helper()
	_, points := telemetrytest.RequestDurationPoints(t, reader)
	statuses := map[string]uint64{}
	for _, p := range points {
		code, _ := p.Attributes.Value(semconv.HTTPResponseStatusCodeKey)
		statuses[code.AsString()] += p.Count
	}
	return statuses
}

func total(statuses map[string]uint64) uint64 {
	var n uint64
	for _, v := range statuses {
		n += v
	}
	return n
}

// TestMiddlewareChainCharacterization pins what the layers around the HTTP middleware do on the official
// endpoints today: which of them serve HTTP/2 without TLS, that CORS answers a preflight before the HTTP server
// metrics see it, and what an oversized body gets and whether the HTTP server metrics record it. Giving the
// endpoints middleware must not change any of it.
func TestMiddlewareChainCharacterization(t *testing.T) {
	ca, err := os.ReadFile(certDir + "ca-cert.pem")
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(ca))

	oversized := `{"flagKey":"a","selector":"` + strings.Repeat("x", 4*requestLimit) + `"}`

	type target struct {
		name, path string
		factory    service.Factory
		tls        bool // the endpoint serves TLS when given a certificate
		config     func(port uint16, cert, key string) map[string]any
		// h2c is whether a prior-knowledge HTTP/2 call without TLS is served.
		h2c bool
		// oversizedStatus is the response to a body above the limit; oversizedRecorded is the status the HTTP server
		// metrics record for it, empty if they record nothing.
		oversizedStatus   int
		oversizedRecorded string
	}
	targets := []target{
		{
			name: "flag evaluation", path: "/flagd.evaluation.v1.Service/ResolveBoolean", factory: flageval.Factory{}, tls: true, h2c: true,
			oversizedStatus: http.StatusTooManyRequests, oversizedRecorded: "429",
			config: func(port uint16, cert, key string) map[string]any {
				return map[string]any{flageval.ConfigKey: flageval.EndpointConfig{Configuration: service.Configuration{
					Port: port, CertPath: cert, KeyPath: key, CORS: []string{"*"}, MaxRequestBodyBytes: requestLimit,
					ServiceName: "flagd",
				}}}
			},
		},
		{
			name: "OFREP", path: "/ofrep/v1/evaluate/flags/a", factory: ofrep.Factory{},
			oversizedStatus: http.StatusRequestEntityTooLarge, oversizedRecorded: "413",
			config: func(port uint16, _, _ string) map[string]any {
				return map[string]any{ofrep.ConfigKey: ofrep.EndpointConfig{
					Service: ofrep.SvcConfiguration{Port: port, ServiceName: "flagd", MaxRequestBodyBytes: requestLimit},
					CORS:    []string{"*"},
				}}
			},
		},
		{
			name: "flag sync", path: "/flagd.sync.v1.FlagSyncService/FetchAllFlags", factory: flagsync.Factory{}, tls: true, h2c: true,
			oversizedStatus: http.StatusTooManyRequests, oversizedRecorded: "429",
			config: func(port uint16, cert, key string) map[string]any {
				return map[string]any{flagsync.ConfigKey: flagsync.SvcConfigurations{
					Port: port, CertPath: cert, KeyPath: key, CORS: []string{"*"}, MaxRequestBodyBytes: requestLimit,
					ServiceName: "flagd",
				}}
			},
		},
	}

	for _, tt := range targets {
		for _, useTLS := range []bool{false, true} {
			if useTLS && !tt.tls {
				continue
			}
			t.Run(fmt.Sprintf("%s tls=%t", tt.name, useTLS), func(t *testing.T) {
				settings, _ := officialSettings(t, tt.factory)
				tel := telemetrytest.New()
				settings.Telemetry = tel
				port := freePort(t)
				cert, key := "", ""
				if useTLS {
					cert, key = certDir+"server-cert.pem", certDir+"server-key.pem"
				}
				settings.Config = tt.config(port, cert, key)
				serveFactory(t, tt.factory, settings)
				waitForPort(t, port)

				scheme, host := "http", "127.0.0.1"
				h2 := &http2.Transport{}
				if useTLS {
					scheme, host = "https", "0.0.0.0" // the name the test certificate is valid for
					h2.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
				} else {
					h2.AllowHTTP = true
					h2.DialTLSContext = func(ctx context.Context, _, addr string, _ *tls.Config) (net.Conn, error) {
						return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
					}
				}
				h2Client := &http.Client{Transport: h2, Timeout: 10 * time.Second}
				url := scheme + "://" + host + ":" + strconv.Itoa(int(port)) + tt.path

				do := func(client *http.Client, req *http.Request) (*http.Response, error) {
					resp, err := client.Do(req)
					if err == nil {
						resp.Body.Close()
					}
					return resp, err
				}

				// an endpoint without h2c is spoken to over HTTP/1.1
				client := h2Client
				if !tt.h2c {
					client = &http.Client{Timeout: 10 * time.Second}
				}

				grpcCall, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte{0, 0, 0, 0, 0}))
				require.NoError(t, err)
				grpcCall.Header.Set("Content-Type", "application/grpc")
				resp, err := do(h2Client, grpcCall)
				if tt.h2c {
					require.NoError(t, err, "a prior-knowledge HTTP/2 call is served")
					require.Equal(t, 2, resp.ProtoMajor)
				} else {
					require.Error(t, err, "a prior-knowledge HTTP/2 call is refused")
				}
				// the metrics are recorded once the handler returns, which may be after the client has its response
				callStatuses := recordedStatuses(t, tel.Metrics)
				if tt.h2c {
					require.Eventually(t, func() bool {
						callStatuses = recordedStatuses(t, tel.Metrics)
						return total(callStatuses) == 1
					}, 5*time.Second, 10*time.Millisecond, "the HTTP server metrics record the served call")
				}
				before := total(callStatuses)

				// CORS answers a preflight before the HTTP server metrics see it
				preflight, err := http.NewRequest(http.MethodOptions, url, nil)
				require.NoError(t, err)
				preflight.Header.Set("Origin", "https://example.com")
				preflight.Header.Set("Access-Control-Request-Method", "POST")
				resp, err = do(client, preflight)
				require.NoError(t, err)
				require.Less(t, resp.StatusCode, 300, "CORS answers the preflight")
				require.NotEmpty(t, resp.Header.Get("Access-Control-Allow-Origin"))
				require.Equal(t, before, total(recordedStatuses(t, tel.Metrics)), "the preflight is not recorded")

				// a body above the limit
				post, err := http.NewRequest(http.MethodPost, url, strings.NewReader(oversized))
				require.NoError(t, err)
				post.Header.Set("Content-Type", "application/json")
				resp, err = do(client, post)
				require.NoError(t, err)
				require.Equal(t, tt.oversizedStatus, resp.StatusCode, "the response to a body above the limit")
				if tt.oversizedRecorded == "" {
					require.Equal(t, before, total(recordedStatuses(t, tel.Metrics)), "the HTTP server metrics do not record it")
				} else {
					require.Eventually(t, func() bool {
						return recordedStatuses(t, tel.Metrics)[tt.oversizedRecorded]-callStatuses[tt.oversizedRecorded] == 1
					}, 5*time.Second, 10*time.Millisecond, "the HTTP server metrics record it under its status")
				}
			})
		}
	}
}
