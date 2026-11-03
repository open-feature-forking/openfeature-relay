package eventsinkexample_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink/eventsinktest"
	"github.com/stretchr/testify/require"

	eventsinkexample "github.com/open-feature/flagd/test/eventsink-example"
)

// hook is a webhook receiver that can stall and refuse.
type hook struct {
	srv     *httptest.Server
	mu      sync.Mutex
	gate    chan struct{} // closed while the receiver answers
	batches [][]events.Event
	status  int // 0 accepts
	raw     [][]map[string]any
}

func newHook(t *testing.T) *hook {
	t.Helper()
	h := &hook{gate: make(chan struct{})}
	close(h.gate)
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hook) serve(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	gate, status := h.gate, h.status
	h.mu.Unlock()
	select {
	case <-gate:
	case <-r.Context().Done():
		return
	}
	if r.Context().Err() != nil {
		return
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	var payload []map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || r.Header.Get("Content-Type") != "application/json" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	batch := make([]events.Event, 0, len(payload))
	for _, p := range payload {
		switch events.Kind(p["kind"].(string)) {
		case events.KindEvaluation:
			batch = append(batch, events.Evaluation{FlagKey: p["flagKey"].(string)})
		case events.KindTracking:
			batch = append(batch, events.Tracking{EventName: p["name"].(string)})
		case events.KindConfigurationChange:
			batch = append(batch, events.ConfigurationChange{Source: p["source"].(string)})
		}
	}
	h.mu.Lock()
	h.batches = append(h.batches, batch)
	h.raw = append(h.raw, payload)
	h.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (h *hook) received(testing.TB) [][]events.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.batches)
}

func (h *hook) stall(testing.TB) func() {
	h.mu.Lock()
	gate := make(chan struct{})
	h.gate = gate
	h.mu.Unlock()
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	return release
}

func (h *hook) refuse(_ testing.TB, permanent bool) func() {
	h.mu.Lock()
	h.status = http.StatusServiceUnavailable
	if permanent {
		h.status = http.StatusBadRequest
	}
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		h.status = 0
		h.mu.Unlock()
	}
}

func (h *hook) inputs() eventsinktest.Inputs {
	return eventsinktest.Inputs{
		Config:        map[string]any{eventsinkexample.SettingURL: h.srv.URL},
		InvalidConfig: map[string]any{eventsinkexample.SettingURL: "ftp://nowhere"},
		FaultyValue:   "ftp://nowhere",
		Received:      h.received,
		Stall:         h.stall,
		Refuse:        h.refuse,
	}
}

// TestContract covers the main scenario's steps 1 to 6: the example is a component module that depends on the
// core module and meets every case of the event sink contract.
func TestContract(t *testing.T) {
	h := newHook(t)
	t.Cleanup(func() { h.stall(t)() })
	eventsinktest.Run(t, eventsinkexample.Factory{}, h.inputs())
}

// TestContractAsNotifier covers A1 and A2: the same sink created for configuration change events only meets the
// contract as a notifier, and an unavailable system is retried while a refusal is dropped.
func TestContractAsNotifier(t *testing.T) {
	h := newHook(t)
	t.Cleanup(func() { h.stall(t)() })
	f := eventsinkexample.Factory{EventKinds: []events.Kind{events.KindConfigurationChange}}
	require.Equal(t, []events.Kind{events.KindConfigurationChange}, f.Kinds())
	eventsinktest.Run(t, f, h.inputs())
}

func TestPostsEveryKindAsJSON(t *testing.T) {
	h := newHook(t)
	bus := events.NewBus()
	p, err := eventsink.NewPipeline(bus, eventsinkexample.Factory{}, eventsink.Settings{Config: map[string]any{
		eventsinkexample.SettingURL: h.srv.URL, eventsink.KeyFlushInterval: time.Hour,
	}})
	require.NoError(t, err)

	now := time.Now()
	bus.Publish(events.Evaluation{Time: now, FlagKey: "flag", FlagSet: "set", Variant: "on", Reason: "STATIC", Value: true, HasValue: true})
	bus.Publish(events.Tracking{Time: now, EventName: "purchase", Value: 9.5, Details: map[string]any{"k": "v"}})
	bus.Publish(events.ConfigurationChange{Time: now, Source: "file", Format: "flagd", FlagSet: "set", Version: "v1", Added: []string{"a"}, Removed: []string{"b"}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, p.Close(ctx))

	h.mu.Lock()
	defer h.mu.Unlock()
	require.Len(t, h.raw, 1, "one request for the batch")
	byKind := map[string]map[string]any{}
	for _, e := range h.raw[0] {
		byKind[e["kind"].(string)] = e
	}
	require.Equal(t, "flag", byKind["evaluation"]["flagKey"])
	require.Equal(t, "set", byKind["evaluation"]["flagSet"])
	require.Equal(t, true, byKind["evaluation"]["value"])
	require.Equal(t, "purchase", byKind["tracking"]["name"])
	require.Equal(t, 9.5, byKind["tracking"]["value"])
	require.Equal(t, "file", byKind["configuration_change"]["source"])
	require.Equal(t, []any{"a"}, byKind["configuration_change"]["added"])
	require.Equal(t, []any{"b"}, byKind["configuration_change"]["removed"])
}

func TestStatusMapping(t *testing.T) {
	for status, want := range map[int]string{
		200: "ok", 204: "ok", 429: "retry", 500: "retry", 503: "retry", 400: "permanent", 401: "permanent", 404: "permanent",
	} {
		h := newHook(t)
		h.status = status
		sink, err := eventsinkexample.Factory{}.Create(eventsink.Settings{Config: map[string]any{eventsinkexample.SettingURL: h.srv.URL}})
		require.NoError(t, err)

		err = sink.Deliver(context.Background(), []events.Event{events.Evaluation{FlagKey: "f"}})
		switch want {
		case "ok":
			require.NoError(t, err, status)
		case "retry":
			require.Error(t, err, status)
			require.False(t, eventsink.IsPermanent(err), status)
		case "permanent":
			require.Error(t, err, status)
			require.True(t, eventsink.IsPermanent(err), status)
		}
	}
}

func TestUnreachableWebhookIsRetried(t *testing.T) {
	h := newHook(t)
	url := h.srv.URL
	h.srv.Close()
	sink, err := eventsinkexample.Factory{}.Create(eventsink.Settings{Config: map[string]any{eventsinkexample.SettingURL: url}})
	require.NoError(t, err)

	err = sink.Deliver(context.Background(), []events.Event{events.Evaluation{FlagKey: "f"}})
	require.Error(t, err)
	require.False(t, eventsink.IsPermanent(err))
}

func TestTimeoutBoundsADelivery(t *testing.T) {
	h := newHook(t)
	t.Cleanup(h.stall(t))
	sink, err := eventsinkexample.Factory{}.Create(eventsink.Settings{Config: map[string]any{
		eventsinkexample.SettingURL: h.srv.URL, eventsinkexample.SettingTimeout: "50ms",
	}})
	require.NoError(t, err)

	began := time.Now()
	err = sink.Deliver(context.Background(), []events.Event{events.Evaluation{FlagKey: "f"}})
	require.Error(t, err)
	require.False(t, eventsink.IsPermanent(err))
	require.Less(t, time.Since(began), 2*time.Second)
	require.True(t, errors.Is(err, context.DeadlineExceeded))
}

func TestInvalidSettingsNameTheFaultyValue(t *testing.T) {
	for name, tt := range map[string]struct {
		config map[string]any
		value  string
	}{
		"no url":         {nil, `"url"`},
		"url type":       {map[string]any{"url": 5}, `"url"`},
		"scheme":         {map[string]any{"url": "ftp://nowhere"}, "ftp://nowhere"},
		"no host":        {map[string]any{"url": "http://"}, "http://"},
		"bad timeout":    {map[string]any{"url": "http://x", "timeout": "soon"}, `"soon"`},
		"zero timeout":   {map[string]any{"url": "http://x", "timeout": "0s"}, `"0s"`},
		"timeout type":   {map[string]any{"url": "http://x", "timeout": []int{1}}, "[1]"},
		"negative value": {map[string]any{"url": "http://x", "timeout": -time.Second}, "-1s"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := eventsinkexample.Factory{}.Create(eventsink.Settings{Config: tt.config})
			require.ErrorContains(t, err, tt.value)
		})
	}
	_, err := eventsinkexample.Factory{}.Create(eventsink.Settings{Config: map[string]any{"url": "http://x", "timeout": 2 * time.Second}})
	require.NoError(t, err)
}

func TestDefaultKinds(t *testing.T) {
	require.ElementsMatch(t,
		[]events.Kind{events.KindConfigurationChange, events.KindEvaluation, events.KindTracking},
		eventsinkexample.Factory{}.Kinds())
	require.Equal(t, "webhook", eventsinkexample.Factory{}.Type())
	require.NoError(t, eventsink.ValidateDeclarations(eventsinkexample.Factory{}))
}
