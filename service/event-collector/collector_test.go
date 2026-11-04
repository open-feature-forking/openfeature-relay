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
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service/servicetest"
	"github.com/stretchr/testify/require"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
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
	port := freePort(t)
	settings := map[string]any{"port": port}
	for k, v := range config {
		settings[k] = v
	}
	bus := events.NewBus()
	endpoint, err := Factory{}.Create(service.Settings{Bus: bus, Logger: logger.NewLogger(nil, false), Config: settings})
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
		error    string
	}{
		"missing flagKey":        {`{"events": [` + valid + `, {"kind": "evaluation"}]}`, 400, map[int]string{1: `needs a "flagKey"`}, ""},
		"missing name":           {`{"events": [{"kind": "tracking"}]}`, 400, map[int]string{0: `needs a "name"`}, ""},
		"unknown kind":           {`{"events": [{"kind": "metric", "flagKey": "x"}]}`, 400, map[int]string{0: `the "kind" must be`}, ""},
		"no kind":                {`{"events": [{"flagKey": "x"}]}`, 400, map[int]string{0: `the "kind" must be`}, ""},
		"not an object":          {`{"events": ["evaluation", 3]}`, 400, map[int]string{0: "must be a JSON object", 1: "must be a JSON object"}, ""},
		"wrong field type":       {`{"events": [{"kind": "evaluation", "flagKey": 5}]}`, 400, map[int]string{0: `"flagKey" must be a string`}, ""},
		"bad time":               {`{"events": [{"kind": "evaluation", "flagKey": "x", "time": "yesterday"}]}`, 400, map[int]string{0: `"time" must be an RFC 3339 time`}, ""},
		"hasValue not a boolean": {`{"events": [{"kind": "evaluation", "flagKey": "x", "hasValue": "yes"}]}`, 400, map[int]string{0: `"hasValue" must be a boolean`}, ""},
		"tracking value":         {`{"events": [{"kind": "tracking", "name": "n", "value": "9"}]}`, 400, map[int]string{0: `must be a number`}, ""},
		"tracking details":       {`{"events": [{"kind": "tracking", "name": "n", "details": [1]}]}`, 400, map[int]string{0: `"details" must be an object`}, ""},
		"every invalid one":      {`{"events": [{"kind": "tracking"}, ` + valid + `, {"kind": "evaluation"}]}`, 400, map[int]string{0: `needs a "name"`, 2: `needs a "flagKey"`}, ""},
		"not JSON":               {`{"events": [`, 400, nil, "events array"},
		"no events array":        {`{"event": []}`, 400, nil, `"events" array`},
		"events not an array":    {`{"events": {}}`, 400, nil, "events array"},
		"body is an array":       {`[]`, 400, nil, "events array"},
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
				require.EqualValues(t, 0, out["accepted"])
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
			require.Empty(t, drain(sub), "a batch with an invalid event publishes nothing, the valid events of it included")
		})
	}
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
