package flagevallegacy

import (
	"context"
	"errors"
	"net/http"
	"testing"

	evaluationV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v1/evaluationv1connect"
	schemaConnectV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/schema/v1/schemav1connect"
	evalV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	schemaV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/schema/v1"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service/servicetest"
	flageval "github.com/open-feature-forking/openfeature-relay/service/flag-evaluation"
	flagdService "github.com/open-feature-forking/openfeature-relay/service/transport"
)

// eventStream is the receiving side of an event stream of either protocol version.
type eventStream interface {
	Receive() bool
	Err() error
	Close() error
}

// contractInputs runs the service endpoint contract on the hosted endpoint, composed with the flag evaluation endpoint
// that serves it, with the clients of one of its services: it names the features the host does not have through the
// contract's inputs, as the host's own contract test does.
func contractInputs(
	openStream func(ctx context.Context, addr string) (eventStream, error),
	resolveBoolean func(ctx context.Context, addr, key, selector string) error,
) servicetest.Inputs {
	return servicetest.Inputs{
		Host:    flageval.Factory{},
		PortKey: "port",
		HTTP:    true,
		Without: []servicetest.Feature{
			servicetest.FeatureDocuments, servicetest.FeatureValidator, servicetest.FeatureListen,
			servicetest.FeatureSendEvents,
		},
		Connect: func(ctx context.Context, addr string) (<-chan struct{}, error) {
			// the event stream stays open until the endpoint ends it; its first message says that it serves
			stream, err := openStream(context.Background(), addr)
			if err != nil {
				return nil, err
			}
			if !stream.Receive() {
				return nil, errors.Join(errors.New("the event stream sent nothing"), stream.Err())
			}
			ended := make(chan struct{})
			go func() {
				defer close(ended)
				for stream.Receive() {
				}
				stream.Close()
			}()
			return ended, nil
		},
		Resolve: func(ctx context.Context, addr, key, selector string) (servicetest.Answer, error) {
			err := resolveBoolean(ctx, addr, key, selector)
			switch connect.CodeOf(err) {
			case connect.CodeNotFound:
				return servicetest.Answer{ErrorCode: model.FlagNotFoundErrorCode}, nil
			default:
				if err != nil {
					return servicetest.Answer{}, err
				}
			}
			return servicetest.Answer{}, nil
		},
	}
}

func withSelectorHeader[T any](msg *T, selector string) *connect.Request[T] {
	req := connect.NewRequest(msg)
	if selector != "" {
		req.Header().Set(flagdService.FLAGD_SELECTOR_HEADER, selector)
	}
	return req
}

// TestConnectContract runs the contract on the hosted endpoint with the clients of schema.v1 and of evaluation.v1.
func TestConnectContract(t *testing.T) {
	t.Run("schema.v1", func(t *testing.T) {
		client := func(addr string) schemaConnectV1.ServiceClient {
			return schemaConnectV1.NewServiceClient(http.DefaultClient, "http://"+addr)
		}
		servicetest.Run(t, Factory{}, contractInputs(
			func(ctx context.Context, addr string) (eventStream, error) {
				return client(addr).EventStream(ctx, connect.NewRequest(&schemaV1.EventStreamRequest{}))
			},
			func(ctx context.Context, addr, key, selector string) error {
				_, err := client(addr).ResolveBoolean(ctx, withSelectorHeader(&schemaV1.ResolveBooleanRequest{FlagKey: key}, selector)) //nolint:staticcheck // frozen with schema.v1
				return err
			},
		))
	})
	t.Run("evaluation.v1", func(t *testing.T) {
		client := func(addr string) evaluationV1.ServiceClient {
			return evaluationV1.NewServiceClient(http.DefaultClient, "http://"+addr)
		}
		servicetest.Run(t, Factory{}, contractInputs(
			func(ctx context.Context, addr string) (eventStream, error) {
				return client(addr).EventStream(ctx, connect.NewRequest(&evalV1.EventStreamRequest{}))
			},
			func(ctx context.Context, addr, key, selector string) error {
				_, err := client(addr).ResolveBoolean(ctx, withSelectorHeader(&evalV1.ResolveBooleanRequest{FlagKey: key}, selector))
				return err
			},
		))
	})
}
