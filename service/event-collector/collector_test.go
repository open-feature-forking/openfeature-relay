package eventcollector

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service/servicetest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/stretchr/testify/require"
)

// reserved are the listeners freePort opened and nobody has taken yet. The collector opens its listener through
// Settings.Listen, which hands it the reserved one, so the port is never released between choosing it and binding it.
var (
	reservedMu sync.Mutex
	reserved   = map[int]net.Listener{}
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	reservedMu.Lock()
	reserved[port] = l
	reservedMu.Unlock()
	t.Cleanup(func() {
		reservedMu.Lock()
		delete(reserved, port)
		reservedMu.Unlock()
		_ = l.Close()
	})
	return port
}

// listenReserved is a service.ListenFunc that returns the listener freePort reserved for the port in address, and opens
// a new one when there is none.
func listenReserved(network, address string) (net.Listener, error) {
	if _, p, err := net.SplitHostPort(address); err == nil {
		if port, err := strconv.Atoi(p); err == nil {
			reservedMu.Lock()
			l, ok := reserved[port]
			delete(reserved, port)
			reservedMu.Unlock()
			if ok {
				return l, nil
			}
		}
	}
	return net.Listen(network, address)
}

// started is a collector serving on a free port, with the bus it publishes to.
type started struct {
	addr string
	bus  *events.Bus
	done chan error
	stop context.CancelFunc
}

func start(t *testing.T, config map[string]any) *started {
	t.Helper()
	return startWith(t, config, nil)
}

// startWith is start with a change to the settings the runtime would inject.
func startWith(t *testing.T, config map[string]any, mutate func(*service.Settings)) *started {
	t.Helper()
	port := freePort(t)
	settings := map[string]any{"port": port}
	for k, v := range config {
		settings[k] = v
	}
	bus := events.NewBus()
	injected := service.Settings{Bus: bus, Logger: logger.NewLogger(nil, false), Config: settings, Listen: listenReserved}
	if mutate != nil {
		mutate(&injected)
	}
	endpoint, err := Factory{}.Create(injected)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	s := &started{addr: fmt.Sprintf("127.0.0.1:%d", port), bus: bus, done: make(chan error, 1), stop: cancel}
	go func() { s.done <- endpoint.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
			t.Error("the collector did not stop")
		}
	})
	require.Eventually(t, func() bool {
		c, err := net.DialTimeout("tcp", s.addr, 100*time.Millisecond)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}, 3*time.Second, 10*time.Millisecond)
	return s
}

func (s *started) post(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post("http://"+s.addr+Path, "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// drain returns the events the subscription holds.
func drain(sub *events.Subscription) []events.Event {
	var got []events.Event
	for {
		select {
		case ev := <-sub.C():
			got = append(got, ev)
		case <-time.After(150 * time.Millisecond):
			return got
		}
	}
}

func subscribe(bus *events.Bus) *events.Subscription {
	return bus.Subscribe(events.SubscribeOptions{Kinds: []events.Kind{events.KindEvaluation, events.KindTracking}, Buffer: 64})
}

// TestCollectorContract runs the service endpoint contract with a port. The collector resolves no flag, serves no
// documents, gives no validator and announces nothing, so it names those features; it collects events.
func TestCollectorContract(t *testing.T) {
	servicetest.Run(t, Factory{}, servicetest.Inputs{
		PortKey: "port",
		Without: []servicetest.Feature{
			servicetest.FeatureDocuments, servicetest.FeatureValidator, servicetest.FeatureListen, servicetest.FeatureHTTP,
		},
		Connect: func(ctx context.Context, addr string) (<-chan struct{}, error) {
			// a keep-alive connection that the endpoint has to end: one request, then the connection stays open
			var conn net.Conn
			var err error
			for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				if conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr); err == nil {
					break
				}
			}
			if err != nil {
				return nil, err
			}
			body := `{"events":[]}`
			_, err = fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
				Path, addr, len(body), body)
			if err != nil {
				return nil, err
			}
			reader := bufio.NewReader(conn)
			resp, err := http.ReadResponse(reader, nil)
			if err != nil {
				return nil, err
			}
			_ = resp.Body.Close()
			ended := make(chan struct{})
			go func() {
				defer close(ended)
				defer conn.Close()
				_, _ = reader.ReadByte()
			}()
			return ended, nil
		},
		SendEvents: func(ctx context.Context, addr string, batch servicetest.Batch) error {
			var items []map[string]any
			for _, e := range batch.Evaluations {
				items = append(items, map[string]any{"kind": "evaluation", "flagKey": e.FlagKey, "reason": e.Reason})
			}
			for _, e := range batch.Trackings {
				items = append(items, map[string]any{"kind": "tracking", "name": e.EventName, "targetingKey": e.TargetingKey})
			}
			body, err := json.Marshal(map[string]any{"events": items})
			if err != nil {
				return err
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+Path, bytes.NewReader(body))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusAccepted {
				return fmt.Errorf("the collector answered %s", resp.Status)
			}
			return nil
		},
	})
}

func TestCollectorPublishesInProcessEvents(t *testing.T) {
	s := start(t, nil)
	sub := subscribe(s.bus)
	defer sub.Close()

	status, out := s.post(t, `{"events": [
		{"kind": "evaluation", "time": "2026-01-02T03:04:05Z", "flagKey": "checkout", "flagSet": "web", "format": "flagd",
		 "variant": "on", "reason": "TARGETING_MATCH", "targetingKey": "user-1", "value": true, "origin": "service"},
		{"kind": "evaluation", "flagKey": "banner", "errorCode": "FLAG_NOT_FOUND", "reason": "ERROR"},
		{"kind": "tracking", "time": "2026-01-02T03:04:06Z", "name": "purchase", "targetingKey": "user-1", "value": 9.5,
		 "details": {"currency": "EUR"}}
	]}`)
	require.Equal(t, http.StatusAccepted, status)
	require.EqualValues(t, 3, out["accepted"])

	got := drain(sub)
	require.Len(t, got, 3)
	first, ok := got[0].(events.Evaluation)
	require.True(t, ok)
	require.Equal(t, events.Evaluation{
		Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), FlagKey: "checkout", FlagSet: "web", Format: "flagd", Endpoint: Type,
		Variant: "on", Reason: "TARGETING_MATCH", TargetingKey: "user-1", Value: true, HasValue: true,
		Origin: events.OriginInProcess, // the origin a client sends is ignored
	}, first)

	second := got[1].(events.Evaluation)
	require.Equal(t, "banner", second.FlagKey)
	require.Equal(t, "FLAG_NOT_FOUND", second.ErrorCode)
	require.False(t, second.HasValue, "no value was sent")
	require.WithinDuration(t, time.Now(), second.Time, 5*time.Second, "the time defaults to the arrival of the batch")
	require.Equal(t, events.OriginInProcess, second.Origin)

	third := got[2].(events.Tracking)
	require.Equal(t, events.Tracking{
		Time: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), EventName: "purchase", TargetingKey: "user-1", Value: 9.5,
		Details: map[string]any{"currency": "EUR"}, Origin: events.OriginInProcess,
	}, third)
}

func TestCollectorRejectsInvalidEvents(t *testing.T) {
	valid := `{"kind": "evaluation", "flagKey": "ok"}`
	tests := map[string]struct {
		body   string
		status int
		// rejected maps the index of each invalid event to a part of its reason
		rejected map[int]string
		// published is the number of valid events of the batch, which are published and counted as accepted
		published int
		error     string
	}{
		"missing flagKey":        {`{"events": [` + valid + `, {"kind": "evaluation"}]}`, 400, map[int]string{1: `needs a "flagKey"`}, 1, ""},
		"missing name":           {`{"events": [{"kind": "tracking"}]}`, 400, map[int]string{0: `needs a "name"`}, 0, ""},
		"unknown kind":           {`{"events": [{"kind": "metric", "flagKey": "x"}]}`, 400, map[int]string{0: `the "kind" must be`}, 0, ""},
		"no kind":                {`{"events": [{"flagKey": "x"}]}`, 400, map[int]string{0: `the "kind" must be`}, 0, ""},
		"not an object":          {`{"events": ["evaluation", 3]}`, 400, map[int]string{0: "must be a JSON object", 1: "must be a JSON object"}, 0, ""},
		"wrong field type":       {`{"events": [{"kind": "evaluation", "flagKey": 5}]}`, 400, map[int]string{0: `"flagKey" must be a string`}, 0, ""},
		"bad time":               {`{"events": [{"kind": "evaluation", "flagKey": "x", "time": "yesterday"}]}`, 400, map[int]string{0: `"time" must be an RFC 3339 time`}, 0, ""},
		"hasValue not a boolean": {`{"events": [{"kind": "evaluation", "flagKey": "x", "hasValue": "yes"}]}`, 400, map[int]string{0: `"hasValue" must be a boolean`}, 0, ""},
		"tracking value":         {`{"events": [{"kind": "tracking", "name": "n", "value": "9"}]}`, 400, map[int]string{0: `must be a number`}, 0, ""},
		"tracking details":       {`{"events": [{"kind": "tracking", "name": "n", "details": [1]}]}`, 400, map[int]string{0: `"details" must be an object`}, 0, ""},
		"every invalid one":      {`{"events": [{"kind": "tracking"}, ` + valid + `, {"kind": "evaluation"}, ` + valid + `]}`, 400, map[int]string{0: `needs a "name"`, 2: `needs a "flagKey"`}, 2, ""},
		"not JSON":               {`{"events": [`, 400, nil, 0, "events array"},
		"no events array":        {`{"event": []}`, 400, nil, 0, `"events" array`},
		"events not an array":    {`{"events": {}}`, 400, nil, 0, "events array"},
		"body is an array":       {`[]`, 400, nil, 0, "events array"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := start(t, nil)
			sub := subscribe(s.bus)
			defer sub.Close()

			status, out := s.post(t, tt.body)
			require.Equal(t, tt.status, status)
			if tt.error != "" {
				require.Contains(t, out["error"], tt.error)
			}
			if tt.rejected != nil {
				require.EqualValues(t, tt.published, out["accepted"], "the answer counts the valid events it published")
				list, _ := out["rejected"].([]any)
				got := map[int]string{}
				for _, r := range list {
					item := r.(map[string]any)
					got[int(item["index"].(float64))] = item["reason"].(string)
				}
				require.Len(t, got, len(tt.rejected), "every invalid event is listed: %v", out)
				for index, part := range tt.rejected {
					require.Contains(t, got[index], part, "event %d", index)
				}
			}
			published := drain(sub)
			require.Len(t, published, tt.published, "the valid events of the batch are published, the invalid ones are not")
			for _, ev := range published {
				require.Equal(t, "ok", ev.(events.Evaluation).FlagKey)
			}
		})
	}
}

func TestCollectorCountsNumbersAsTheRemoteEndpointsDo(t *testing.T) {
	s := start(t, nil)
	sub := subscribe(s.bus)
	defer sub.Close()

	status, _ := s.post(t, `{"events": [
		{"kind": "evaluation", "flagKey": "int", "value": 5},
		{"kind": "evaluation", "flagKey": "big", "value": 9007199254740993},
		{"kind": "evaluation", "flagKey": "float", "value": 5.5},
		{"kind": "evaluation", "flagKey": "fraction-zero", "value": 5.0},
		{"kind": "evaluation", "flagKey": "exponent", "value": 1e2},
		{"kind": "evaluation", "flagKey": "object", "value": {"n": 1, "list": [2, {"m": 3}]}},
		{"kind": "tracking", "name": "t", "value": 7, "details": {"count": 4, "nested": {"x": 1}}}
	]}`)
	require.Equal(t, http.StatusAccepted, status)

	got := drain(sub)
	require.Len(t, got, 7)
	values := map[string]any{}
	for _, ev := range got[:6] {
		e := ev.(events.Evaluation)
		values[e.FlagKey] = e.Value
	}
	require.Equal(t, int64(5), values["int"], "a whole number stays an integer")
	require.Equal(t, int64(9007199254740993), values["big"], "and keeps its digits")
	require.Equal(t, 5.5, values["float"])
	require.Equal(t, float64(5), values["fraction-zero"])
	require.Equal(t, float64(100), values["exponent"])
	require.Equal(t, map[string]any{"n": float64(1), "list": []any{float64(2), map[string]any{"m": float64(3)}}}, values["object"],
		"numbers inside an object are floats, as in a flag configuration")

	tracking := got[6].(events.Tracking)
	require.Equal(t, float64(7), tracking.Value)
	require.Equal(t, map[string]any{"count": float64(4), "nested": map[string]any{"x": float64(1)}}, tracking.Details)
}

// TestCollectorIsWrappedByTheDistributionsMiddleware pins that Settings.WrapHTTP applies to POST /events/v1: the
// collector puts events on the bus, so an authentication middleware of the distribution has to see every request and be
// able to refuse it. CORS stays outside it, so a preflight never reaches the middleware.
func TestCollectorIsWrappedByTheDistributionsMiddleware(t *testing.T) {
	var seen atomic.Int64
	s := startWith(t, map[string]any{"cors": []string{"https://app.example.com"}}, func(settings *service.Settings) {
		settings.HTTPMiddleware = func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen.Add(1)
				if r.Header.Get("X-Api-Key") != "secret" {
					http.Error(w, "key required", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
			})
		}
	})
	sub := subscribe(s.bus)
	defer sub.Close()
	const body = `{"events": [{"kind": "evaluation", "flagKey": "f"}]}`

	post := func(key string) int {
		req, err := http.NewRequest(http.MethodPost, "http://"+s.addr+Path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("X-Api-Key", key)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}

	require.Equal(t, http.StatusUnauthorized, post(""), "the middleware refuses the request")
	require.Equal(t, http.StatusUnauthorized, post("wrong"))
	require.Empty(t, drain(sub), "a refused request publishes nothing")
	require.EqualValues(t, 2, seen.Load(), "the middleware saw both requests")

	require.Equal(t, http.StatusAccepted, post("secret"))
	require.Len(t, drain(sub), 1)
	require.EqualValues(t, 3, seen.Load())

	preflight, err := http.NewRequest(http.MethodOptions, "http://"+s.addr+Path, nil)
	require.NoError(t, err)
	preflight.Header.Set("Origin", "https://app.example.com")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(preflight)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, "https://app.example.com", resp.Header.Get("Access-Control-Allow-Origin"))
	require.EqualValues(t, 3, seen.Load(), "CORS answers a preflight before the middleware")
}

// instrumented is a telemetry provider that counts the requests that pass its HTTP wrappers and keeps their options.
type instrumented struct {
	telemetry.Provider
	metrics, spans atomic.Int64
	options        atomic.Pointer[telemetry.HTTPMetricsOptions]
	operation      atomic.Pointer[string]
}

func (p *instrumented) HTTPServerMetrics(h http.Handler, opts telemetry.HTTPMetricsOptions) http.Handler {
	p.options.Store(&opts)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.metrics.Add(1)
		h.ServeHTTP(w, r)
	})
}

func (p *instrumented) HTTPServerSpans(h http.Handler, operation string) http.Handler {
	p.operation.Store(&operation)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.spans.Add(1)
		h.ServeHTTP(w, r)
	})
}

// TestCollectorIsInstrumentedThroughTheTelemetryProvider pins that every request passes the provider's HTTP metrics and
// span wrappers, with the collector's route and flagd's service name.
func TestCollectorIsInstrumentedThroughTheTelemetryProvider(t *testing.T) {
	provider := &instrumented{Provider: telemetry.Noop()}
	s := startWith(t, nil, func(settings *service.Settings) { settings.Telemetry = provider })

	status, _ := s.post(t, `{"events": [{"kind": "evaluation", "flagKey": "f"}]}`)
	require.Equal(t, http.StatusAccepted, status)
	status, _ = s.post(t, `{"events": [{"kind": "evaluation"}]}`)
	require.Equal(t, http.StatusBadRequest, status)

	require.EqualValues(t, 2, provider.metrics.Load(), "each request is counted in the HTTP metrics")
	require.EqualValues(t, 2, provider.spans.Load(), "and recorded as a span")
	require.Equal(t, telemetry.HTTPMetricsOptions{Service: "flagd", Route: Path}, *provider.options.Load())
	require.Equal(t, "flagd.event-collector", *provider.operation.Load())
}

// Go allows 4096 bytes beyond MaxHeaderBytes, so the header here is larger than that.
func TestCollectorHonoursTheHeaderLimit(t *testing.T) {
	s := start(t, map[string]any{"max-request-header-bytes": 1024})
	sub := subscribe(s.bus)
	defer sub.Close()

	req, err := http.NewRequest(http.MethodPost, "http://"+s.addr+Path, strings.NewReader(`{"events": [{"kind": "evaluation", "flagKey": "f"}]}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Padding", strings.Repeat("x", 20000))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusRequestHeaderFieldsTooLarge, resp.StatusCode)
	require.Empty(t, drain(sub))
}

func TestCollectorAnswersOtherRequestsPlainly(t *testing.T) {
	s := start(t, map[string]any{"max-request-body-bytes": 200})
	sub := subscribe(s.bus)
	defer sub.Close()

	get, err := http.Get("http://" + s.addr + Path)
	require.NoError(t, err)
	get.Body.Close()
	require.Equal(t, http.StatusMethodNotAllowed, get.StatusCode)

	other, err := http.Post("http://"+s.addr+"/events/v2", "application/json", strings.NewReader(`{"events":[]}`))
	require.NoError(t, err)
	other.Body.Close()
	require.Equal(t, http.StatusNotFound, other.StatusCode)

	text, err := http.Post("http://"+s.addr+Path, "text/plain", strings.NewReader(`{"events":[]}`))
	require.NoError(t, err)
	text.Body.Close()
	require.Equal(t, http.StatusUnsupportedMediaType, text.StatusCode)

	big := `{"events":[{"kind":"evaluation","flagKey":"` + strings.Repeat("x", 300) + `"}]}`
	status, out := s.post(t, big)
	require.Equal(t, http.StatusRequestEntityTooLarge, status)
	require.Contains(t, out["error"], "200 bytes")
	require.Empty(t, drain(sub))
}

func TestCollectorServeBlocksWithoutPort(t *testing.T) {
	bus := events.NewBus()
	for name, config := range map[string]map[string]any{"no settings": nil, "port 0": {"port": 0}, "other settings only": {"cors": []string{"*"}}} {
		t.Run(name, func(t *testing.T) {
			endpoint, err := Factory{}.Create(service.Settings{Bus: bus, Config: config})
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- endpoint.Serve(ctx) }()

			select {
			case err := <-done:
				t.Fatalf("expected Serve to block until its context ends\nobserved: it returned %v", err)
			case <-time.After(300 * time.Millisecond):
			}
			cancel()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("expected Serve to return when its context ends\nobserved: it was still blocked")
			}
		})
	}
}

// TestListenSeamUsedByTheCollector pins that the collector opens its listener through Settings.Listen, and serves on
// the listener that function returns.
func TestListenSeamUsedByTheCollector(t *testing.T) {
	var asked []string
	var mu sync.Mutex
	listen := func(network, address string) (net.Listener, error) {
		mu.Lock()
		asked = append(asked, network+" "+address)
		mu.Unlock()
		return listenReserved(network, address)
	}
	s := startWith(t, nil, func(settings *service.Settings) { settings.Listen = listen })

	_, port, err := net.SplitHostPort(s.addr)
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"tcp :" + port}, asked)
}

// TestCollectorHasNoServerWithoutAPort pins that an inert collector builds no HTTP server, so Serve cannot open a port.
func TestCollectorHasNoServerWithoutAPort(t *testing.T) {
	for name, config := range map[string]map[string]any{"no settings": nil, "port 0": {"port": 0}, "other settings only": {"cors": []string{"*"}}} {
		created, err := Factory{}.Create(service.Settings{Bus: events.NewBus(), Config: config})
		require.NoError(t, err, name)
		require.Nil(t, created.(*endpoint).server, name)
	}
	created, err := Factory{}.Create(service.Settings{Bus: events.NewBus(), Config: map[string]any{"port": 8017}})
	require.NoError(t, err)
	require.NotNil(t, created.(*endpoint).server, "a collector with a port serves")
}

func TestCollectorCORSAndLimits(t *testing.T) {
	s := start(t, map[string]any{"cors": []string{"https://app.example.com"}})

	preflight, err := http.NewRequest(http.MethodOptions, "http://"+s.addr+Path, nil)
	require.NoError(t, err)
	preflight.Header.Set("Origin", "https://app.example.com")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(preflight)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, "https://app.example.com", resp.Header.Get("Access-Control-Allow-Origin"))

	foreign, err := http.NewRequest(http.MethodOptions, "http://"+s.addr+Path, nil)
	require.NoError(t, err)
	foreign.Header.Set("Origin", "https://evil.example.com")
	foreign.Header.Set("Access-Control-Request-Method", "POST")
	resp, err = http.DefaultClient.Do(foreign)
	require.NoError(t, err)
	resp.Body.Close()
	require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))

	// the default size limit is OFREP's 1 MB; a limit of 0 switches it off
	s2 := start(t, nil)
	large := `{"events":[{"kind":"tracking","name":"` + strings.Repeat("x", DefaultMaxRequestBodyBytes) + `"}]}`
	status, _ := s2.post(t, large)
	require.Equal(t, http.StatusRequestEntityTooLarge, status)

	s3 := start(t, map[string]any{"max-request-body-bytes": 0})
	status, _ = s3.post(t, large)
	require.Equal(t, http.StatusAccepted, status)
}

func TestCollectorSettings(t *testing.T) {
	cfg, err := decodeConfig(map[string]any{
		"port": 8017, "service-name": "flagd", "cors": []string{"*"}, "max-request-body-bytes": 10, "max-request-header-bytes": 20,
	})
	require.NoError(t, err)
	require.Equal(t, config{port: 8017, serviceName: "flagd", cors: []string{"*"}, maxRequestBodyBytes: 10, maxRequestHeaderBytes: 20}, cfg)

	cfg, err = decodeConfig(nil)
	require.NoError(t, err)
	require.Equal(t, int64(DefaultMaxRequestBodyBytes), cfg.maxRequestBodyBytes)
	require.Equal(t, "flagd", cfg.serviceName, "the service name defaults to flagd's, as for the other endpoints")

	for name, tt := range map[string]struct {
		settings map[string]any
		want     string
	}{
		"unknown key":    {map[string]any{"prot": 1}, `unknown setting "prot"`},
		"port type":      {map[string]any{"port": "high"}, `setting "port"`},
		"port range":     {map[string]any{"port": 70000}, `setting "port"`},
		"negative limit": {map[string]any{"max-request-body-bytes": -1}, "must not be negative"},
	} {
		_, err := Factory{}.Create(service.Settings{Bus: events.NewBus(), Config: tt.settings})
		require.ErrorContains(t, err, tt.want, name)
		require.ErrorContains(t, err, Type, name)
	}

	_, err = Factory{}.Create(service.Settings{Config: map[string]any{}})
	require.ErrorContains(t, err, "needs the event bus")
}

func TestCollectorDeclaresNoNeed(t *testing.T) {
	require.Equal(t, service.Needs{}, Factory{}.Needs())
	require.Equal(t, Type, NewFactory().Type())
	require.NoError(t, service.ValidateDeclarations(NewFactory()))
}
