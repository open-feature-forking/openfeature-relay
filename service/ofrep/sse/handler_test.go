package sse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/launchdarkly/eventsource"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindSelector mounts the service behind a middleware that binds every request to the selector expression.
func bindSelector(t *testing.T, svc *Service, expression string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	svc.Register(mux, testSSEPath)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(middleware.BindSelector(r.Context(), expression)))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// A misconfigured binding is the operator's error: 500, and no subscription.
func TestService_Handler_InvalidBoundSelectorReturns500(t *testing.T) {
	for _, withLogger := range []bool{true, false} {
		log := logger.NewLogger(nil, false)
		s, err := store.NewStore(log, []string{"src1"})
		require.NoError(t, err)
		cfg := Config{HeartbeatInterval: time.Hour}
		if withLogger {
			cfg.Logger = log
		}
		svc := New(s, cfg)
		ts := bindSelector(t, svc, "bogusKey=1")

		resp, err := http.Get(streamURL(ts, ""))
		require.NoError(t, err)
		_ = resp.Body.Close()

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode, "logger set: %v", withLogger)
		assert.Empty(t, svc.Tracker().Channels())
	}
}

// After Close the tracker takes no new subscriber: 503, not a stream that never delivers.
func TestService_Handler_ClosedServiceReturns503(t *testing.T) {
	log := logger.NewLogger(nil, false)
	s, err := store.NewStore(log, []string{"src1"})
	require.NoError(t, err)
	svc := New(s, Config{Logger: log, HeartbeatInterval: time.Hour})
	ts := newStreamServer(t, svc)
	svc.Close()

	resp, err := http.Get(streamURL(ts, ""))
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// A request bound by middleware subscribes on a channel named after the effective selector, whatever the client sent.
func TestService_Handler_BoundSelectorNamesTheChannel(t *testing.T) {
	log := logger.NewLogger(nil, false)
	s, err := store.NewStore(log, []string{"src1"})
	require.NoError(t, err)
	svc := New(s, Config{Logger: log, HeartbeatInterval: time.Hour})
	ts := bindSelector(t, svc, "flagSetId=fs1")

	stream, err := eventsource.SubscribeWithURL(streamURL(ts, ""))
	require.NoError(t, err)
	defer stream.Close()

	want := "bound:" + effectiveFS1(t)
	assert.Eventually(t, func() bool {
		channels := svc.Tracker().Channels()
		return len(channels) == 1 && channels[0] == want
	}, 3*time.Second, 5*time.Millisecond)
}

func TestChannel(t *testing.T) {
	effective := mustSelector(t, "flagSetId=fs1")
	log := func(s store.Selector) string { return s.ToLogString() }
	bound := middleware.BindSelector(context.Background(), "flagSetId=fs1")

	assert.Equal(t, "source=a", Channel(context.Background(), "source=a", effective), "unbound: the client's expression")
	assert.Equal(t, "", Channel(context.Background(), "", effective))
	assert.Equal(t, "bound:"+log(effective), Channel(bound, "source=a", effective), "bound: the effective selector")
	assert.Equal(t, Channel(bound, "", effective), Channel(bound, "source=a", effective), "the client's expression does not matter")
}

func effectiveFS1(t *testing.T) string {
	t.Helper()
	s := mustSelector(t, "flagSetId=fs1")
	return s.ToLogString()
}
