package ofoperator

import (
	"fmt"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"go.uber.org/zap"
	"k8s.io/client-go/dynamic"
)

// Provider is the provider name of FeatureFlag sources.
const Provider = "kubernetes"

// Prefix is the prefix of a FeatureFlag source's URI; the namespace and name of the resource follow it.
const Prefix = "core.openfeature.dev/"

// ClientBuilder builds the Kubernetes client the provider reads FeatureFlag resources with.
type ClientBuilder interface {
	GetK8sClient() (dynamic.Interface, error)
}

// Factory creates the streaming sync providers of FeatureFlag sources. A distribution sets the builder of the
// Kubernetes client; without one the factory reads the cluster configuration from the environment.
type Factory struct {
	Client ClientBuilder
}

var _ sync.StreamingFactory = Factory{}

// Type is the provider name.
func (Factory) Type() string { return Provider }

// Schemes is the prefix of FeatureFlag sources; it is not a URI scheme.
func (Factory) Schemes() []string { return []string{Prefix} }

// ParseURI strips the prefix, which leaves <namespace>/<name>.
func (Factory) ParseURI(uri string) (sync.SourceConfig, bool) {
	if resource, ok := strings.CutPrefix(uri, Prefix); ok {
		return sync.SourceConfig{URI: resource, Provider: Provider}, true
	}
	return sync.SourceConfig{}, false
}

// CreateStreaming creates the provider of a source.
func (f Factory) CreateStreaming(settings sync.Settings) (sync.StreamingSyncProvider, error) {
	builder := f.Client
	if builder == nil {
		builder = KubernetesClientBuilder{}
	}
	client, err := builder.GetK8sClient()
	if err != nil {
		return nil, fmt.Errorf("error creating kubernetes clients: %w", err)
	}
	s := NewK8sSync(
		settings.Logger.WithFields(zap.String("component", "sync"), zap.String("sync", "kubernetes")),
		settings.Source.URI,
		client,
	)
	s.SyncMetricsRecorder = settings.Metrics
	return s, nil
}

// NewFactories returns the factories of the Kubernetes sync component, as a generated main of a distribution calls
// it. They read the cluster configuration from the environment.
func NewFactories() []sync.Factory { return []sync.Factory{Factory{}} }
