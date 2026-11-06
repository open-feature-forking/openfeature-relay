package flagevallegacy

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	evaluationV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v1/evaluationv1connect"
	schemaConnectV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/schema/v1/schemav1connect"
	evalV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	schemaV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/schema/v1"
	"connectrpc.com/connect"
	evaluatormock "github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/mock"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	iservice "github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/structpb"
)

// eventStreamClient is the receiving side of one EventStream call, whatever the protocol version.
type eventStreamClient struct {
	receive func() (eventType string, data map[string]any, ok bool)
	close   func()
}

func structData(data *structpb.Struct) map[string]any {
	if data == nil {
		return nil
	}
	return data.AsMap()
}

// TestEventStreamHandlers pins the deprecated schema.v1 and evaluation.v1 event streams on the subscriptions of the host:
// the initial provider_ready, a configuration_change after a store update, and the end of the stream when the client
// cancels.
func TestEventStreamHandlers(t *testing.T) {
	const source = "source1"

	tests := []struct {
		name string
		open func(ctx context.Context, url string) (*eventStreamClient, error)
	}{
		{
			name: "schema.v1",
			open: func(ctx context.Context, url string) (*eventStreamClient, error) {
				stream, err := schemaConnectV1.NewServiceClient(http.DefaultClient, url).
					EventStream(ctx, connect.NewRequest(&schemaV1.EventStreamRequest{}))
				if err != nil {
					return nil, err
				}
				return &eventStreamClient{
					receive: func() (string, map[string]any, bool) {
						if !stream.Receive() {
							return "", nil, false
						}
						return stream.Msg().Type, structData(stream.Msg().Data), true
					},
					close: func() { _ = stream.Close() },
				}, nil
			},
		},
		{
			name: "evaluation.v1",
			open: func(ctx context.Context, url string) (*eventStreamClient, error) {
				stream, err := evaluationV1.NewServiceClient(http.DefaultClient, url).
					EventStream(ctx, connect.NewRequest(&evalV1.EventStreamRequest{}))
				if err != nil {
					return nil, err
				}
				return &eventStreamClient{
					receive: func() (string, map[string]any, bool) {
						if !stream.Receive() {
							return "", nil, false
						}
						return stream.Msg().Type, structData(stream.Msg().Data), true
					},
					close: func() { _ = stream.Close() },
				}, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := store.NewStore(logger.NewLogger(nil, false), []string{source})
			require.NoError(t, err)
			url := serveComposed(t, evaluatormock.NewMockIEvaluator(gomock.NewController(t)), s, nil, iservice.Configuration{})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client, err := tt.open(ctx, url)
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
		})
	}
}
