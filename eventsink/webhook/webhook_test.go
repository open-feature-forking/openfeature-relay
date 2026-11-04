package webhook_test

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
	"github.com/open-feature-forking/openfeature-relay/eventsink/webhook"
	"github.com/stretchr/testify/require"
)

// hook is a webhook receiver that can stall and refuse.
type hook struct {
	srv     *httptest.Server
	mu      sync.Mutex
	gate    chan struct{} // closed while the receiver answers
	batches [][]events.Event
	status  int // 0 accepts
	headers []http.Header
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
	h.headers = append(h.headers, r.Header.Clone())
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
		Config:        map[string]any{webhook.SettingURL: h.srv.URL},
		InvalidConfig: map[string]any{webhook.SettingURL: "ftp://nowhere"},
		FaultyValue:   "ftp://nowhere",
		Received:      h.received,
		Stall:         h.stall,
		Refuse:        h.refuse,
	}
}

// TestContract: the webhook event sink meets every case of the event sink contract.
func TestContract(t *testing.T) {
	h := newHook(t)
	t.Cleanup(func() { h.stall(t)() })
	eventsinktest.Run(t, webhook.Factory{}, h.inputs())
}

// TestContractAsNotifier covers A1 and A2: the same sink created for configuration change events only meets the
// contract as a notifier, and an unavailable system is retried while a refusal is dropped.
func TestContractAsNotifier(t *testing.T) {
	h := newHook(t)
	t.Cleanup(func() { h.stall(t)() })
	f := webhook.Factory{EventKinds: []events.Kind{events.KindConfigurationChange}}
	require.Equal(t, []events.Kind{events.KindConfigurationChange}, f.Kinds())
	eventsinktest.Run(t, f, h.inputs())
}

func TestPostsEveryKindAsJSON(t *testing.T) {
	h := newHook(t)
	bus := events.NewBus()
	p, err := eventsink.NewPipeline(bus, webhook.Factory{}, eventsink.Settings{Config: map[string]any{
		webhook.SettingURL: h.srv.URL, eventsink.KeyFlushInterval: time.Hour,
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
		sink, err := webhook.Factory{}.Create(eventsink.Settings{Config: map[string]any{webhook.SettingURL: h.srv.URL}})
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
	sink, err := webhook.Factory{}.Create(eventsink.Settings{Config: map[string]any{webhook.SettingURL: url}})
	require.NoError(t, err)

	err = sink.Deliver(context.Background(), []events.Event{events.Evaluation{FlagKey: "f"}})
	require.Error(t, err)
	require.False(t, eventsink.IsPermanent(err))
}

func TestTimeoutBoundsADelivery(t *testing.T) {
	h := newHook(t)
	t.Cleanup(h.stall(t))
	sink, err := webhook.Factory{}.Create(eventsink.Settings{Config: map[string]any{
		webhook.SettingURL: h.srv.URL, webhook.SettingTimeout: "50ms",
	}})
	require.NoError(t, err)

	result := make(chan error, 1)
	go func() {
		result <- sink.Deliver(context.Background(), []events.Event{events.Evaluation{FlagKey: "f"}})
	}()
	select {
	case err = <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the delivery to return after its 50ms timeout\nobserved: still blocked after 2s")
	}
	require.Error(t, err)
	require.False(t, eventsink.IsPermanent(err))
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
			_, err := webhook.Factory{}.Create(eventsink.Settings{Config: tt.config})
			require.ErrorContains(t, err, tt.value)
		})
	}
	_, err := webhook.Factory{}.Create(eventsink.Settings{Config: map[string]any{"url": "http://x", "timeout": 2 * time.Second}})
	require.NoError(t, err)
}

func TestDefaultKinds(t *testing.T) {
	require.ElementsMatch(t,
		[]events.Kind{events.KindConfigurationChange, events.KindEvaluation, events.KindTracking},
		webhook.Factory{}.Kinds())
	require.Equal(t, "webhook", webhook.Factory{}.Type())
	require.NoError(t, eventsink.ValidateDeclarations(webhook.Factory{}))
}

func TestConfiguredHeadersAreSentWithEveryRequest(t *testing.T) {
	h := newHook(t)
	sink, err := webhook.Factory{}.Create(eventsink.Settings{Config: map[string]any{
		webhook.SettingURL:     h.srv.URL,
		webhook.SettingHeaders: map[string]any{"Authorization": "Bearer s3cret", "x-tenant": "acme"},
	}})
	require.NoError(t, err)

	for range 2 {
		require.NoError(t, sink.Deliver(context.Background(), []events.Event{events.Evaluation{FlagKey: "f"}}))
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	require.Len(t, h.headers, 2)
	for _, got := range h.headers {
		require.Equal(t, "Bearer s3cret", got.Get("Authorization"))
		require.Equal(t, "acme", got.Get("X-Tenant"))
		require.Equal(t, "application/json", got.Get("Content-Type"))
	}
}

func TestHeadersAsStringMap(t *testing.T) {
	h := newHook(t)
	sink, err := webhook.Factory{}.Create(eventsink.Settings{Config: map[string]any{
		webhook.SettingURL: h.srv.URL, webhook.SettingHeaders: map[string]string{"Authorization": "Bearer s3cret"},
	}})
	require.NoError(t, err)
	require.NoError(t, sink.Deliver(context.Background(), []events.Event{events.Evaluation{FlagKey: "f"}}))
	h.mu.Lock()
	defer h.mu.Unlock()
	require.Equal(t, "Bearer s3cret", h.headers[0].Get("Authorization"))
}

func TestInvalidHeadersNameTheSettingNeverTheValue(t *testing.T) {
	const secret = "s3cret-token"
	for name, tt := range map[string]struct {
		headers any
		named   string
	}{
		"not a mapping":      {[]string{secret}, `"headers"`},
		"value not a string": {map[string]any{"Authorization": []string{secret}}, `"Authorization"`},
		"bad name":           {map[string]any{"Bad Name": secret}, `"Bad Name"`},
		"empty name":         {map[string]any{"": secret}, `"headers"`},
		"content type":       {map[string]any{"Content-Type": secret}, `"Content-Type"`},
		"host":               {map[string]any{"host": secret}, `"host"`},
		"line break":         {map[string]any{"X-Token": secret + "\r\nX-Evil: 1"}, `"X-Token"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := webhook.Factory{}.Create(eventsink.Settings{Config: map[string]any{
				webhook.SettingURL: "http://x", webhook.SettingHeaders: tt.headers,
			}})
			require.ErrorContains(t, err, tt.named)
			require.ErrorContains(t, err, "headers")
			require.NotContains(t, err.Error(), secret)
		})
	}
}

func TestDeliveryErrorsNeverCarryHeaderValues(t *testing.T) {
	const secret = "Bearer s3cret-token"
	h := newHook(t)
	h.status = http.StatusUnauthorized
	cfg := map[string]any{webhook.SettingURL: h.srv.URL, webhook.SettingHeaders: map[string]any{"Authorization": secret}}
	sink, err := webhook.Factory{}.Create(eventsink.Settings{Config: cfg})
	require.NoError(t, err)

	err = sink.Deliver(context.Background(), []events.Event{events.Evaluation{FlagKey: "f"}})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "s3cret-token")

	h.srv.Close()
	err = sink.Deliver(context.Background(), []events.Event{events.Evaluation{FlagKey: "f"}})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "s3cret-token")
}

func TestUnknownSettingIsRefusedNamingItAndTheValidOnes(t *testing.T) {
	_, err := webhook.Factory{}.Create(eventsink.Settings{Config: map[string]any{
		webhook.SettingURL: "http://x", "timout": "1s", "hedaers": map[string]any{},
	}})
	require.Error(t, err)
	require.ErrorContains(t, err, `"hedaers", "timout"`)
	for _, valid := range []string{"url", "timeout", "headers", "bufferSize", "batchSize", "flushInterval", "maxAttempts", "kinds"} {
		require.ErrorContains(t, err, valid)
	}
}

func TestCoreOwnedSettingsAreAccepted(t *testing.T) {
	_, err := webhook.Factory{}.Create(eventsink.Settings{Config: map[string]any{
		webhook.SettingURL: "http://x", eventsink.KeyBufferSize: 10, eventsink.KeyBatchSize: 5,
		eventsink.KeyFlushInterval: "1s", eventsink.KeyMaxAttempts: 2, eventsink.KeyKinds: []any{"evaluation"},
	}})
	require.NoError(t, err)
}

func TestRedirectIsARefusalNotFollowed(t *testing.T) {
	for _, status := range []int{
		http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect,
	} {
		var hits int
		var mu sync.Mutex
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			hits++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}))
		redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, status)
		}))
		sink, err := webhook.Factory{}.Create(eventsink.Settings{Config: map[string]any{webhook.SettingURL: redirecting.URL}})
		require.NoError(t, err)

		err = sink.Deliver(context.Background(), []events.Event{events.Evaluation{FlagKey: "f"}})
		require.Error(t, err, status)
		require.True(t, eventsink.IsPermanent(err), "a redirect is refused for good: %d", status)
		mu.Lock()
		require.Zero(t, hits, "the batch must not be sent to the redirect target: %d", status)
		mu.Unlock()
		redirecting.Close()
		target.Close()
	}
}
