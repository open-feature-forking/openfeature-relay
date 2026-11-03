package flagdgrpc

import (
	"regexp"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc/credentials"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// Provider is the provider name of flagd gRPC sync sources.
const Provider = "grpc"

// customResolver matches the targets that name a gRPC name resolver.
var customResolver = regexp.MustCompile("^" + SupportedScheme)

// Factory creates the streaming sync providers of flagd gRPC sync sources. A distribution sets what it injects into
// every provider: the dial options of the client connection, such as its instrumentation, and the recorder of the
// stream's lifecycle.
type Factory struct {
	DialOptions   []grpc.DialOption
	StreamMetrics StreamMetricsRecorder
}

var _ sync.StreamingFactory = Factory{}

// Type is the provider name.
func (Factory) Type() string { return Provider }

// Schemes are the prefixes of gRPC sources: grpc://, grpcs:// and the names of the name resolvers.
func (Factory) Schemes() []string {
	return []string{Prefix, PrefixSecure, "envoy", "dns", "uds", "xds"}
}

// ParseURI strips grpc:// and grpcs://, which also turns on TLS, and keeps a target that names a resolver.
func (Factory) ParseURI(uri string) (sync.SourceConfig, bool) {
	switch {
	case strings.HasPrefix(uri, Prefix):
		return sync.SourceConfig{URI: strings.TrimPrefix(uri, Prefix), Provider: Provider}, true
	case strings.HasPrefix(uri, PrefixSecure):
		return sync.SourceConfig{URI: strings.TrimPrefix(uri, PrefixSecure), Provider: Provider, TLS: true}, true
	case customResolver.MatchString(uri):
		return sync.SourceConfig{URI: uri, Provider: Provider}, true
	}
	return sync.SourceConfig{}, false
}

// CreateStreaming creates the provider of a source.
func (f Factory) CreateStreaming(settings sync.Settings) (sync.StreamingSyncProvider, error) {
	config := settings.Source
	return &Sync{
		URI: config.URI,
		Logger: settings.Logger.WithFields(
			zap.String("component", "sync"),
			zap.String("sync", "grpc"),
		),
		CredentialBuilder:   &credentials.CredentialBuilder{},
		CertPath:            config.CertPath,
		ProviderID:          config.ProviderID,
		Secure:              config.TLS,
		Selector:            config.Selector,
		MaxMsgSize:          config.MaxMsgSize,
		IncrementalUpdates:  config.IncrementalUpdates,
		Headers:             config.Headers,
		SyncMetricsRecorder: settings.Metrics,

		StreamMetricsRecorder: f.StreamMetrics,
		DialOptions:           f.DialOptions,
	}, nil
}

// NewFactories returns the factories of the flagd gRPC sync component, as a generated main of a distribution calls
// it.
func NewFactories() []sync.Factory { return []sync.Factory{Factory{}} }
