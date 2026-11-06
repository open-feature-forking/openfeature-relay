package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	evaluationV2 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v2/evaluationv2connect"
	evalV2 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v2"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	iservice "github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/stretchr/testify/require"
)

// eventStreamClient is the receiving side of one EventStream call, whatever the protocol version.
type eventStreamClient struct {
	receive func() (eventType string, data map[string]any, ok bool)
	err     func() error
	close   func()
}

// TestEventStreamHandlers pins the evaluation.v2 event stream: the initial
// provider_ready, a configuration_change after a store update, and the end of the stream when the client cancels.
func TestEventStreamHandlers(t *testing.T) {
	const source = "source1"
	log := logger.NewLogger(nil, false)

	tests := []struct {
		name string
		// serve mounts the handler on a mux over the given eventing configuration and returns a function that opens a stream
		serve func(mux *http.ServeMux, events IEvents) func(ctx context.Context, url string) (*eventStreamClient, error)
	}{
		{
			name: "evaluation.v2",
			serve: func(mux *http.ServeMux, events IEvents) func(context.Context, string) (*eventStreamClient, error) {
				path, h := evaluationV2.NewServiceHandler(NewFlagEvaluationServiceV2(log, nil, events, nil, nil, nil, 0))
				mux.Handle(path, h)
				return func(ctx context.Context, url string) (*eventStreamClient, error) {
					stream, err := evaluationV2.NewServiceClient(http.DefaultClient, url).
						EventStream(ctx, connect.NewRequest(&evalV2.EventStreamRequest{}))
					if err != nil {
						return nil, err
					}
					return &eventStreamClient{
						receive: func() (string, map[string]any, bool) {
							if !stream.Receive() {
								return "", nil, false
							}
							msg := stream.Msg()
							var data map[string]any
							if msg.Data != nil {
								data = msg.Data.AsMap()
							}
							return msg.Type, data, true
						},
						err:   stream.Err,
						close: func() { _ = stream.Close() },
					}, nil
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, s := newTestEventingConfig(t, []string{source})
			mux := http.NewServeMux()
			open := tt.serve(mux, events)
			handlerDone := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(handlerDone)
				mux.ServeHTTP(w, r)
			}))
			defer srv.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client, err := open(ctx, srv.URL)
			require.NoError(t, err)
			defer client.close()

			type event struct {
				typ  string
				data map[string]any
			}
			received := make(chan event, 64)
			go func() {
				for {
					typ, data, ok := client.receive()
					if !ok {
						received <- event{typ: "<end>"}
						return
					}
					received <- event{typ, data}
				}
			}()
			next := func() event {
				t.Helper()
				select {
				case e := <-received:
					return e
				case <-time.After(5 * time.Second):
					t.Fatal("timeout waiting for an event")
					return event{}
				}
			}

			ready := next()
			require.Equal(t, string(iservice.ProviderReady), ready.typ, "ready comes first")
			require.Empty(t, ready.data, "ready carries no data")

			// The subscription takes its first store snapshot on its own goroutine and ignores it, so an update that
			// lands before the snapshot is never announced. Change a probe flag until one is announced.
			for i := 0; ; i++ {
				s.Update(source, []model.Flag{{Key: "probe", DefaultVariant: fmt.Sprint(i)}}, model.Metadata{}, false)
				select {
				case e := <-received:
					require.Equal(t, string(iservice.ConfigurationChange), e.typ)
				case <-time.After(20 * time.Millisecond):
					continue
				}
				break
			}

			s.Update(source, []model.Flag{{Key: "flag1", DefaultVariant: "off"}}, model.Metadata{}, false)
			var change event
			for { // an announcement of an earlier probe may still be queued ahead of the one under test
				change = next()
				if flags, _ := change.data["flags"].(map[string]any); flags["flag1"] != nil {
					break
				}
			}
			require.Equal(t, string(iservice.ConfigurationChange), change.typ, "a store update is announced")
			require.Equal(t, map[string]any{"flags": map[string]any{
				"flag1": map[string]any{"type": string(model.NotificationCreate)},
				"probe": map[string]any{"type": string(model.NotificationDelete)},
			}}, change.data)

			cancel()
			require.Equal(t, "<end>", next().typ, "the stream ends on cancel")
			select {
			case <-handlerDone:
			case <-time.After(5 * time.Second):
				t.Fatal("the handler did not return")
			}
			// the subscription's goroutine removes its entry as its last act
			require.Eventually(t, func() bool {
				events.mu.RLock()
				defer events.mu.RUnlock()
				return len(events.subs) == 0
			}, 2*time.Second, time.Millisecond, "the handler unsubscribes")
		})
	}
}
