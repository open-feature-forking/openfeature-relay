package ofrep

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mock "github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/mock"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// openSSEStream serves handler over HTTP, opens the event stream on it and returns the body once the stream is
// established, with a channel that closes when the handler's goroutine returned.
func openSSEStream(t *testing.T, handler http.Handler) (body io.ReadCloser, handlerDone <-chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+ssePath, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return resp.Body, done
}

// requireStreamEnds fails unless the client sees the stream end and the handler's goroutine exits.
func requireStreamEnds(t *testing.T, body io.Reader, handlerDone <-chan struct{}) {
	t.Helper()
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_, _ = io.Copy(io.Discard, bufio.NewReader(body))
	}()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the event stream is still open after Close")
	}
	select {
	case <-handlerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler's goroutine did not exit after Close")
	}
}

func newSSEStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.NewStore(logger.NewLogger(nil, false), []string{"src1"})
	require.NoError(t, err)
	return s
}

// TestServiceCloseEndsOpenEventStream: Close on a service that is never started ends a stream a client holds open.
func TestServiceCloseEndsOpenEventStream(t *testing.T) {
	svc, err := NewOfrepService(mock.NewMockIEvaluator(gomock.NewController(t)), newSSEStore(t), nil, SvcConfiguration{
		Logger: logger.NewLogger(nil, false), ServiceName: testServiceName, SSEEnabled: true, SSEInactivityDelaySec: 60,
	}, nil, nil)
	require.NoError(t, err)

	body, handlerDone := openSSEStream(t, svc.server.Handler)
	require.Eventually(t, func() bool { return len(svc.sse.Tracker().Channels()) == 1 }, 3*time.Second, 5*time.Millisecond)

	require.NoError(t, svc.Close())
	requireStreamEnds(t, body, handlerDone)
	require.Empty(t, svc.sse.Tracker().Channels(), "the subscription is forgotten")

	require.NoError(t, svc.Close(), "Close is safe to repeat")
}

// TestServiceCloseWithoutSSE: a service without an event stream has nothing to stop.
func TestServiceCloseWithoutSSE(t *testing.T) {
	svc, err := NewOfrepService(mock.NewMockIEvaluator(gomock.NewController(t)), nil, nil, SvcConfiguration{
		Logger: logger.NewLogger(nil, false), ServiceName: testServiceName,
	}, nil, nil)
	require.NoError(t, err)
	require.NoError(t, svc.Close())
}

// TestFactoryCloseEndsOpenEventStream: the endpoint the factory made closes like the service it wraps, as the
// runtime does for an endpoint it never served.
func TestFactoryCloseEndsOpenEventStream(t *testing.T) {
	ep, err := Factory{}.Create(service.Settings{
		Store:    newSSEStore(t),
		Resolver: mock.NewMockIEvaluator(gomock.NewController(t)),
		Logger:   logger.NewLogger(nil, false),
		Formats:  []string{"flagd"},
		Config: map[string]any{ConfigKey: EndpointConfig{
			Service: SvcConfiguration{ServiceName: testServiceName, SSEEnabled: true, SSEInactivityDelaySec: 60},
		}},
	})
	require.NoError(t, err)
	e, ok := ep.(endpoint)
	require.True(t, ok)

	body, handlerDone := openSSEStream(t, e.svc.server.Handler)
	require.Eventually(t, func() bool { return len(e.svc.sse.Tracker().Channels()) == 1 }, 3*time.Second, 5*time.Millisecond)

	closer, ok := ep.(interface{ Close() error })
	require.True(t, ok, "the endpoint is closable")
	require.NoError(t, closer.Close())
	requireStreamEnds(t, body, handlerDone)
}

// TestFactoryCreateErrors: an endpoint without a resolver, or with a malformed setting, is not created.
func TestFactoryCreateErrors(t *testing.T) {
	log := logger.NewLogger(nil, false)
	_, err := Factory{}.Create(service.Settings{Logger: log})
	require.ErrorContains(t, err, "needs a resolver")

	_, err = Factory{}.Create(service.Settings{
		Logger:   log,
		Resolver: mock.NewMockIEvaluator(gomock.NewController(t)),
		Config:   map[string]any{"port": "not a number"},
	})
	require.ErrorContains(t, err, Type)
}
