package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	evaluationV1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v1/evaluationv1connect"
	evalV1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service/servicetest"
	flagdService "github.com/open-feature/flagd/flagd/pkg/service"
)

// TestConnectContract runs the service endpoint contract on the flag evaluation endpoint. It names the features it
// does not have through the contract's inputs: it serves no documents, collects no events, gives no validator,
// announces its changes from the flags table and not from the bus (UC-004 Q7), and does not honour a selector bound
// by middleware yet (UC-005).
func TestConnectContract(t *testing.T) {
	client := func(addr string) evaluationV1.ServiceClient {
		return evaluationV1.NewServiceClient(http.DefaultClient, "http://"+addr)
	}

	servicetest.Run(t, Factory{}, servicetest.Inputs{
		PortKey: "port",
		Without: []servicetest.Feature{
			servicetest.FeatureDocuments, servicetest.FeatureValidator, servicetest.FeatureListen,
			servicetest.FeatureSendEvents, servicetest.FeatureHTTP,
		},
		Connect: func(ctx context.Context, addr string) (<-chan struct{}, error) {
			// the event stream stays open until the endpoint ends it; its first message says that it serves
			stream, err := client(addr).EventStream(context.Background(), connect.NewRequest(&evalV1.EventStreamRequest{}))
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
			req := connect.NewRequest(&evalV1.ResolveBooleanRequest{FlagKey: key})
			if selector != "" {
				req.Header().Set(flagdService.FLAGD_SELECTOR_HEADER, selector)
			}
			_, err := client(addr).ResolveBoolean(ctx, req)
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
	})
}
