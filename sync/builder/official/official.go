// Package official lists the official sync components: their factories and the factories of the fetch triggers they
// attach. The sync builder dispatches to whatever factories it is given and imports none of these modules; a
// distribution that wants the official components imports this package.
package official

import (
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/sync/azblob"
	"github.com/open-feature-forking/openfeature-relay/sync/file"
	flagdgrpc "github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc"
	"github.com/open-feature-forking/openfeature-relay/sync/gcs"
	httpSync "github.com/open-feature-forking/openfeature-relay/sync/http"
	ofoperator "github.com/open-feature-forking/openfeature-relay/sync/openfeature-operator"
	"github.com/open-feature-forking/openfeature-relay/sync/s3"
	googlegrpc "google.golang.org/grpc"
)

// Options are what a distribution injects into the official sync components.
type Options struct {
	// StreamMetrics records the lifecycle of the gRPC sync streams; nil takes them from Settings.Telemetry.
	StreamMetrics flagdgrpc.StreamMetricsRecorder
	// GRPCDialOptions are added to the client connection of every gRPC sync source.
	GRPCDialOptions []googlegrpc.DialOption
	// K8sClient builds the Kubernetes client; nil reads the cluster configuration from the environment.
	K8sClient ofoperator.ClientBuilder
}

// Factories returns the official sync components, in the order the Operator is shown their provider names.
func Factories() []sync.Factory { return With(Options{}) }

// With returns the official sync components with what a distribution injects into them.
func With(opts Options) []sync.Factory {
	return []sync.Factory{
		file.Factory{},
		file.FSNotifyTriggerFactory(),
		file.FileInfoTriggerFactory(),
		ofoperator.Factory{Client: opts.K8sClient},
		httpSync.Factory{},
		flagdgrpc.Factory{DialOptions: opts.GRPCDialOptions, StreamMetrics: opts.StreamMetrics},
		gcs.NewFactory(),
		azblob.NewFactory(),
		s3.NewFactory(),
	}
}
