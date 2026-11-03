package sync

import (
	"context"
	"net/http"
	"testing"

	"buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/sync/v1/syncv1connect"
	syncpb "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"connectrpc.com/connect"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service/servicetest"
)

// TestFlagSyncContract runs the service endpoint contract on the flag sync endpoint: it declares no needs, so it
// is given no resolver and no evaluator. It names the features it does not have through the contract's inputs: it
// serves the flags of flagd's format, not the documents of every format (UC-017), gives no validator from document
// versions, announces its changes from the flags table, and collects no events.
func TestFlagSyncContract(t *testing.T) {
	servicetest.Run(t, Factory{}, servicetest.Inputs{
		PortKey: "port",
		Without: []servicetest.Feature{
			servicetest.FeatureDocuments, servicetest.FeatureValidator, servicetest.FeatureListen,
			servicetest.FeatureSendEvents, servicetest.FeatureHTTP,
		},
		Connect: func(_ context.Context, addr string) (<-chan struct{}, error) {
			client := syncv1connect.NewFlagSyncServiceClient(http.DefaultClient, "http://"+addr)
			stream, err := client.SyncFlags(context.Background(), connect.NewRequest(&syncpb.SyncFlagsRequest{}))
			if err != nil {
				return nil, err
			}
			// the stream's first message says that the endpoint serves
			if !stream.Receive() {
				return nil, stream.Err()
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
	})
}
