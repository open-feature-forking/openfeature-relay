//go:build unix

package runtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	flagdcomponents "github.com/open-feature/flagd/flagd/components"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	flageval "github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation"
	"github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation/ofrep"
	flagsync "github.com/open-feature/flagd/flagd/pkg/service/flag-sync"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

const certDir = "../service/flag-sync/test-cert/"

// officialSettings builds the settings of an official endpoint over a store that holds two flags.
func officialSettings(t *testing.T, factory service.Factory) (service.Settings, *events.Bus) {
	t.Helper()
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"src"})
	require.NoError(t, err)
	_, err = flagdformat.NewFormat(st, log).Accept(context.Background(), configformat.Input{
		Source: "src",
		Data: `{"flags":{
			"a":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}},
			"b":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`,
	})
	require.NoError(t, err)

	bus := events.NewBus()
	inputs, err := newEndpointInputs(log, st, bus, []service.Factory{factory}, []string{"flagd"}, []evaluator.Factory{flagdeval.NewFactory()}, nil, false)
	require.NoError(t, err)
	settings := inputs.settings(factory)
	initialized := make(chan struct{})
	close(initialized)
	settings.Initialized = initialized
	return settings, bus
}

func serveFactory(t *testing.T, factory service.Factory, settings service.Settings) {
	t.Helper()
	endpoint, err := factory.Create(settings)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- endpoint.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(15 * time.Second):
			t.Error("Serve did not return")
		}
	})
}

func waitForPort(t *testing.T, port uint16) {
	t.Helper()
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}, 10*time.Second, 25*time.Millisecond)
}

// TestOfficialEndpointsPublishOncePerFlag pins that the injected resolver publishes one evaluation event for each
// resolved flag, and that the endpoints publish nothing by hand: a single flag and a bulk request, on flag
// evaluation and OFREP. Step 6 reuses this test for its own check.
func TestOfficialEndpointsPublishOncePerFlag(t *testing.T) {
	post := func(url, body string, header map[string]string) {
		req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		for k, v := range header {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, url)
	}

	tests := []struct {
		name       string
		factory    service.Factory
		config     func(port uint16) map[string]any
		header     map[string]string
		singleBody string
		single     string
		bulk       string
	}{
		{
			name:    "flag evaluation",
			factory: flageval.Factory{},
			config: func(port uint16) map[string]any {
				return map[string]any{flageval.ConfigKey: flageval.EndpointConfig{Configuration: service.Configuration{Port: port}}}
			},
			header:     map[string]string{"Content-Type": "application/json"},
			singleBody: `{"flagKey":"a"}`,
			single:     "/flagd.evaluation.v1.Service/ResolveBoolean",
			bulk:       "/flagd.evaluation.v1.Service/ResolveAll",
		},
		{
			name:    "OFREP",
			factory: ofrep.Factory{},
			config: func(port uint16) map[string]any {
				return map[string]any{ofrep.ConfigKey: ofrep.EndpointConfig{Service: ofrep.SvcConfiguration{Port: port, ServiceName: "flagd"}}}
			},
			header:     map[string]string{},
			singleBody: "{}",
			single:     "/ofrep/v1/evaluate/flags/a",
			bulk:       "/ofrep/v1/evaluate/flags",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings, bus := officialSettings(t, tt.factory)
			sub := bus.Subscribe(events.SubscribeOptions{Kinds: []events.Kind{events.KindEvaluation}, Buffer: 16})
			defer sub.Close()
			port := freePort(t)
			settings.Config = tt.config(port)
			serveFactory(t, tt.factory, settings)
			waitForPort(t, port)
			base := fmt.Sprintf("http://127.0.0.1:%d", port)

			count := func() int {
				time.Sleep(150 * time.Millisecond)
				n := 0
				for {
					select {
					case <-sub.C():
						n++
					default:
						return n
					}
				}
			}

			post(base+tt.single, tt.singleBody, tt.header)
			require.Equal(t, 1, count(), "one event for one resolved flag")

			post(base+tt.bulk, "{}", tt.header)
			require.Equal(t, 2, count(), "one event for each of the two flags of a bulk request")
		})
	}
}

// requestLog records the requests a test middleware sees.
type requestLog struct {
	seen atomic.Int64
	h2   atomic.Int64
	grpc atomic.Int64
}

func (l *requestLog) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.seen.Add(1)
		if r.ProtoMajor == 2 {
			l.h2.Add(1)
		}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			l.grpc.Add(1)
		}
		next.ServeHTTP(w, r)
	})
}

// TestHTTPMiddlewareSeesH2CRequests pins that the middleware of the distribution sees a prior-knowledge HTTP/2 gRPC
// call, in plaintext and over TLS, on flag evaluation and flag sync, and that a CORS preflight never reaches it.
func TestHTTPMiddlewareSeesH2CRequests(t *testing.T) {
	ca, err := os.ReadFile(certDir + "ca-cert.pem")
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(ca))

	for _, endpoint := range []struct {
		name, path string
		factory    service.Factory
		config     func(port uint16, cert, key string) map[string]any
	}{
		{
			name: "flag evaluation", path: "/flagd.evaluation.v1.Service/ResolveBoolean", factory: flageval.Factory{},
			config: func(port uint16, cert, key string) map[string]any {
				return map[string]any{flageval.ConfigKey: flageval.EndpointConfig{Configuration: service.Configuration{
					Port: port, CertPath: cert, KeyPath: key, CORS: []string{"*"},
				}}}
			},
		},
		{
			name: "flag sync", path: "/flagd.sync.v1.FlagSyncService/FetchAllFlags", factory: flagsync.Factory{},
			config: func(port uint16, cert, key string) map[string]any {
				return map[string]any{flagsync.ConfigKey: flagsync.SvcConfigurations{
					Port: port, CertPath: cert, KeyPath: key, CORS: []string{"*"},
				}}
			},
		},
	} {
		for _, useTLS := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s tls=%t", endpoint.name, useTLS), func(t *testing.T) {
				settings, _ := officialSettings(t, endpoint.factory)
				log := &requestLog{}
				settings.HTTPMiddleware = log.middleware
				port := freePort(t)
				cert, key := "", ""
				if useTLS {
					cert, key = certDir+"server-cert.pem", certDir+"server-key.pem"
				}
				settings.Config = endpoint.config(port, cert, key)
				serveFactory(t, endpoint.factory, settings)

				transport := &http2.Transport{}
				scheme := "http"
				if useTLS {
					scheme = "https"
					transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
				} else {
					transport.AllowHTTP = true
					transport.DialTLSContext = func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
						return (&net.Dialer{}).DialContext(ctx, network, addr)
					}
				}
				client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
				host := "127.0.0.1"
				if useTLS {
					host = "0.0.0.0" // the name the test certificate is valid for
				}
				url := scheme + "://" + host + ":" + strconv.Itoa(int(port)) + endpoint.path

				call := func(newRequest func() *http.Request) *http.Response {
					var resp *http.Response
					var err error
					for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
						if resp, err = client.Do(newRequest()); err == nil {
							break
						}
					}
					require.NoError(t, err)
					resp.Body.Close()
					return resp
				}

				call(func() *http.Request {
					req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte{0, 0, 0, 0, 0}))
					require.NoError(t, err)
					req.Header.Set("Content-Type", "application/grpc")
					return req
				})
				require.Equal(t, int64(1), log.seen.Load(), "the middleware saw the call")
				require.Equal(t, int64(1), log.h2.Load(), "as an HTTP/2 request")
				require.Equal(t, int64(1), log.grpc.Load(), "of gRPC")

				resp := call(func() *http.Request {
					req, err := http.NewRequest(http.MethodOptions, url, nil)
					require.NoError(t, err)
					req.Header.Set("Origin", "https://example.com")
					req.Header.Set("Access-Control-Request-Method", "POST")
					return req
				})
				require.Less(t, resp.StatusCode, 300, "CORS answered the preflight")
				require.Equal(t, int64(1), log.seen.Load(), "the preflight never reached the middleware")
			})
		}
	}
}

// TestOFREPChainOrder pins S2's CORS → HTTPMiddleware → router for OFREP: a request reaches the middleware of the
// distribution, and a CORS preflight, which CORS answers, does not. Were the middleware outside CORS, an
// authentication middleware would answer the browsers' preflights.
func TestOFREPChainOrder(t *testing.T) {
	settings, _ := officialSettings(t, ofrep.Factory{})
	log := &requestLog{}
	settings.HTTPMiddleware = log.middleware
	port := freePort(t)
	settings.Config = map[string]any{ofrep.ConfigKey: ofrep.EndpointConfig{
		Service: ofrep.SvcConfiguration{Port: port, ServiceName: "flagd"}, CORS: []string{"*"},
	}}
	serveFactory(t, ofrep.Factory{}, settings)
	waitForPort(t, port)
	url := fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/evaluate/flags/a", port)

	resp, err := http.Post(url, "application/json", strings.NewReader("{}")) //nolint:noctx // local test server
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, int64(1), log.seen.Load(), "the middleware saw the request")

	req, err := http.NewRequest(http.MethodOptions, url, nil) //nolint:noctx // local test server
	require.NoError(t, err)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Less(t, resp.StatusCode, 300, "CORS answered the preflight")
	require.Equal(t, int64(1), log.seen.Load(), "the preflight never reached the middleware")
}

// TestRuntimeStartsAnyEndpointSet pins that the runtime serves whichever endpoints the distribution passes: OFREP
// alone, flag sync alone with no evaluator built, and all three.
func TestRuntimeStartsAnyEndpointSet(t *testing.T) {
	tests := []struct {
		name      string
		endpoints []service.Factory
		evaluator bool
		check     func(t *testing.T, p ports)
	}{
		{
			name: "OFREP alone", endpoints: []service.Factory{ofrep.Factory{}}, evaluator: true,
			check: func(t *testing.T, p ports) {
				resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/ofrep/v1/evaluate/flags/f1", p.ofrep), "application/json", strings.NewReader("{}")) //nolint:noctx // local
				require.NoError(t, err)
				resp.Body.Close()
				require.Equal(t, http.StatusOK, resp.StatusCode)
			},
		},
		{
			name: "flag sync alone", endpoints: []service.Factory{flagsync.Factory{}}, evaluator: false,
			check: func(t *testing.T, p ports) {
				conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p.sync), time.Second)
				require.NoError(t, err)
				conn.Close()
			},
		},
		{
			name: "all three", endpoints: flagdcomponents.Endpoints(), evaluator: true,
			check: func(t *testing.T, p ports) {
				for _, port := range []uint16{p.service, p.ofrep, p.sync} {
					conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
					require.NoError(t, err)
					conn.Close()
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPorts(t)
			cfg := characterizedConfig(t, p)
			rt, err := FromConfig(logger.NewLogger(nil, false), "test", cfg, Components{
				Sync:                 flagdcomponents.Official().Sync,
				ConfigurationFormats: flagdcomponents.Official().ConfigurationFormats, Evaluators: flagdcomponents.Official().Evaluators, Endpoints: tt.endpoints,
			})
			require.NoError(t, err)
			require.Equal(t, tt.evaluator, rt.Evaluator != nil, "an evaluator is built only for an endpoint that needs one")
			require.Len(t, rt.Endpoints, len(tt.endpoints))

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- rt.start(ctx) }()
			require.Eventually(t, func() bool {
				resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", p.management)) //nolint:noctx // local
				if err != nil {
					return false
				}
				resp.Body.Close()
				return resp.StatusCode == http.StatusOK
			}, 20*time.Second, 50*time.Millisecond)

			tt.check(t, p)

			cancel()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(20 * time.Second):
				t.Fatal("start did not return")
			}
		})
	}
}
