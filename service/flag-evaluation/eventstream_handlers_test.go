package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	evaluationV2 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v2/evaluationv2connect"
	schemaConnectV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/schema/v1/schemav1connect"
	evalV2 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v2"
	schemaV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/schema/v1"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	iservice "github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/stretchr/testify/require"
)

// eventStreamClient is the receiving side of one EventStream call, whatever the protocol version.
type eventStreamClient struct {
	receive func() (eventType string, ok bool)
	err     func() error
	close   func()
}

// TestEventStreamHandlers pins the deprecated schema.v1 and the evaluation.v2 event streams: the initial
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
			name: "schema.v1",
			serve: func(mux *http.ServeMux, events IEvents) func(context.Context, string) (*eventStreamClient, error) {
				path, h := schemaConnectV1.NewServiceHandler(NewOldFlagEvaluationService(log, nil, events, nil, nil))
				mux.Handle(path, h)
				return func(ctx context.Context, url string) (*eventStreamClient, error) {
					stream, err := schemaConnectV1.NewServiceClient(http.DefaultClient, url).
						EventStream(ctx, connect.NewRequest(&schemaV1.EventStreamRequest{}))
					if err != nil {
						return nil, err
					}
					return &eventStreamClient{
						receive: func() (string, bool) {
							if !stream.Receive() {
								return "", false
							}
							return stream.Msg().Type, true
						},
						err:   stream.Err,
						close: func() { _ = stream.Close() },
					}, nil
				}
			},
		},
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
						receive: func() (string, bool) {
							if !stream.Receive() {
								return "", false
							}
							return stream.Msg().Type, true
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
			srv := httptest.NewServer(mux)
			defer srv.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client, err := open(ctx, srv.URL)
			require.NoError(t, err)
			defer client.close()

			next := func() string {
				t.Helper()
				got := make(chan string, 1)
				go func() {
					typ, ok := client.receive()
					if !ok {
						typ = "<end>"
					}
					got <- typ
				}()
				select {
				case typ := <-got:
					return typ
				case <-time.After(5 * time.Second):
					t.Fatal("timeout waiting for an event")
					return ""
				}
			}

			require.Equal(t, string(iservice.ProviderReady), next(), "ready comes first")

			require.Eventually(t, func() bool { // the handler subscribes before it sends ready, so the watch is up soon
				events.mu.RLock()
				defer events.mu.RUnlock()
				return len(events.subs) == 1
			}, 2*time.Second, 5*time.Millisecond)
			time.Sleep(100 * time.Millisecond) // let the subscription goroutine take its first store snapshot
			s.Update(source, []model.Flag{{Key: "flag1", DefaultVariant: "off"}}, model.Metadata{}, false)
			require.Equal(t, string(iservice.ConfigurationChange), next(), "a store update is announced")

			cancel()
			require.Equal(t, "<end>", next(), "the stream ends on cancel")
			require.Eventually(t, func() bool {
				events.mu.RLock()
				defer events.mu.RUnlock()
				return len(events.subs) == 0
			}, 2*time.Second, 5*time.Millisecond, "the handler unsubscribes")
		})
	}
}
