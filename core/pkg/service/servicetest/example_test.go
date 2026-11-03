package servicetest_test

import (
	"context"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service/servicetest"
)

// extensionEndpointFactory is the Extension Author's own service endpoint factory.
var extensionEndpointFactory service.Factory

func ExampleRun() {
	_ = func(t *testing.T) {
		servicetest.Run(t, extensionEndpointFactory, servicetest.Inputs{
			PortKey: "port",
			Config:  map[string]any{},
			Connect: func(ctx context.Context, addr string) (<-chan struct{}, error) {
				// open a connection that stays open, and return a channel that closes when the endpoint ends it
				return nil, nil
			},
			// the endpoint serves no documents, no validators and no announcements, and collects no events
			Without: []servicetest.Feature{
				servicetest.FeatureDocuments, servicetest.FeatureValidator, servicetest.FeatureListen,
				servicetest.FeatureSendEvents, servicetest.FeatureHTTP,
			},
		})
	}
}
