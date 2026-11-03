package builder

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/fetching"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/polling"
	"github.com/open-feature-forking/openfeature-relay/sync/azblob"
	"github.com/open-feature-forking/openfeature-relay/sync/file"
	flagdgrpc "github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc"
	"github.com/open-feature-forking/openfeature-relay/sync/gcs"
	httpSync "github.com/open-feature-forking/openfeature-relay/sync/http"
	ofoperator "github.com/open-feature-forking/openfeature-relay/sync/openfeature-operator"
	"github.com/open-feature-forking/openfeature-relay/sync/s3"
	googlegrpc "google.golang.org/grpc"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	syncProviderFile       = file.Provider
	syncProviderFsNotify   = file.FSNOTIFY
	syncProviderFileInfo   = file.FILEINFO
	syncProviderGrpc       = flagdgrpc.Provider
	syncProviderKubernetes = ofoperator.Provider
	syncProviderHTTP       = httpSync.Provider
	syncProviderGcs        = gcs.Provider
	syncProviderAzblob     = azblob.Provider
	syncProviderS3         = s3.Provider
)

type ISyncBuilder interface {
	SyncFromURI(uri string, logger *logger.Logger) (sync.ISync, error)
	SyncsFromConfig(sourceConfig []sync.SourceConfig, logger *logger.Logger) ([]sync.ISync, error)
}

// OfficialOptions are what a distribution injects into the official sync components.
type OfficialOptions struct {
	// StreamMetrics records the lifecycle of the gRPC sync streams.
	StreamMetrics flagdgrpc.StreamMetricsRecorder
	// GRPCDialOptions are added to the client connection of every gRPC sync source.
	GRPCDialOptions []googlegrpc.DialOption
	// K8sClient builds the Kubernetes client; nil reads the cluster configuration from the environment.
	K8sClient ofoperator.ClientBuilder
}

// Official returns the official sync components: their factories and the factories of the fetch triggers they
// attach, in the order the Operator is shown their provider names.
func Official() []sync.Factory {
	return OfficialWith(OfficialOptions{})
}

// OfficialWith returns the official sync components with what a distribution injects into them.
func OfficialWith(opts OfficialOptions) []sync.Factory {
	k8s := opts.K8sClient
	if k8s == nil {
		k8s = &KubernetesClientBuilder{}
	}
	return []sync.Factory{
		file.Factory{},
		file.FSNotifyTriggerFactory(),
		file.FileInfoTriggerFactory(),
		ofoperator.Factory{Client: k8s},
		httpSync.Factory{},
		flagdgrpc.Factory{DialOptions: opts.GRPCDialOptions, StreamMetrics: opts.StreamMetrics},
		gcs.NewFactory(),
		azblob.NewFactory(),
		s3.NewFactory(),
	}
}

// SyncBuilder creates the sync providers of sources from the factories of the sync components it knows.
type SyncBuilder struct {
	factories        []sync.Factory
	k8sClientBuilder IK8sClientBuilder

	// SyncMetricsRecorder, when non-nil, is passed to every sync provider so they can
	// record the universal client-side sync metrics
	SyncMetricsRecorder sync.MetricsRecorder
}

// clientFunc adapts a function to a client builder.
type clientFunc func() (dynamic.Interface, error)

func (f clientFunc) GetK8sClient() (dynamic.Interface, error) { return f() }

// NewSyncBuilder creates a builder for the given factories; without any it knows the official components.
func NewSyncBuilder(factories ...sync.Factory) *SyncBuilder {
	sb := &SyncBuilder{k8sClientBuilder: &KubernetesClientBuilder{}}
	if len(factories) == 0 {
		factories = OfficialWith(OfficialOptions{K8sClient: clientFunc(func() (dynamic.Interface, error) {
			return sb.k8sClientBuilder.GetK8sClient()
		})})
	}
	sb.factories = factories
	return sb
}

// SyncFromURI creates the sync provider of a file or FeatureFlag source given as a --uri; the configuration proxy
// uses it.
func (sb *SyncBuilder) SyncFromURI(uri string, logger *logger.Logger) (sync.ISync, error) {
	for _, provider := range []string{syncProviderFile, syncProviderKubernetes} {
		f, ok := sb.schemeFactory(provider)
		if !ok {
			continue
		}
		if source, claimed := f.ParseURI(uri); claimed {
			return sb.syncFromConfig(source, logger)
		}
	}
	return nil, fmt.Errorf("unrecognized URI: %s", uri)
}

func (sb *SyncBuilder) SyncsFromConfig(sourceConfigs []sync.SourceConfig, logger *logger.Logger) ([]sync.ISync, error) {
	syncImpls := make([]sync.ISync, len(sourceConfigs))
	for i, syncProvider := range sourceConfigs {
		syncImpl, err := sb.syncFromConfig(syncProvider, logger)
		if err != nil {
			return nil, fmt.Errorf("could not create sync provider: %w", err)
		}
		syncImpls[i] = syncImpl
	}
	return syncImpls, nil
}

func (sb *SyncBuilder) syncFromConfig(sourceConfig sync.SourceConfig, log *logger.Logger) (sync.ISync, error) {
	resolution, err := sb.Resolve(sourceConfig, log)
	if err != nil {
		return nil, err
	}
	if resolution.Streaming != nil {
		log.Debug(fmt.Sprintf("using %s sync-provider for: %s", sourceConfig.Provider, sourceConfig.URI))
		return resolution.Streaming, nil
	}

	triggers := make([]sync.Trigger, len(resolution.Triggers))
	for i, config := range resolution.Triggers {
		if triggers[i], err = sb.createTrigger(config, resolution, log); err != nil {
			return nil, err
		}
	}
	log.Debug(fmt.Sprintf("using %s sync-provider for: %s", sourceConfig.Provider, sourceConfig.URI))
	return fetching.New(fetching.Options{
		Fetcher:            resolution.Fetcher,
		Triggers:           triggers,
		Logger:             log,
		Source:             resolution.Source.URI,
		SourceType:         sourceType(resolution.Factory),
		Metrics:            sb.SyncMetricsRecorder,
		Interval:           time.Duration(resolution.Interval) * time.Second,
		Timeout:            time.Duration(sourceConfig.TimeoutS) * time.Second,
		IncrementalUpdates: sourceConfig.IncrementalUpdates,
	}), nil
}

// createTrigger creates the fetch trigger a source names: the core's poller, or the trigger of a factory.
func (sb *SyncBuilder) createTrigger(config sync.TriggerConfig, resolution Resolution, log *logger.Logger) (sync.Trigger, error) {
	if config.Type == polling.TriggerType {
		poller, err := polling.NewIntervalTrigger(resolution.Interval, resolution.Source.IntervalSeed)
		if err != nil {
			return nil, fmt.Errorf("invalid %s sync configuration: %w", resolution.Source.Provider, err)
		}
		return poller, nil
	}
	for _, f := range sb.factories {
		if tf, ok := f.(sync.TriggerFactory); ok && tf.Type() == config.Type {
			return tf.CreateTrigger(sync.Settings{Source: resolution.Source, Logger: log, Config: config.Config})
		}
	}
	return nil, sb.checkTriggerType(config.Type)
}

// checkTriggerType fails for a fetch trigger type no factory provides, naming the ones available.
func (sb *SyncBuilder) checkTriggerType(triggerType string) error {
	for _, t := range sb.triggerTypes() {
		if t == triggerType {
			return nil
		}
	}
	return fmt.Errorf("unknown fetch trigger type %q, available: %s", triggerType, strings.Join(sb.triggerTypes(), ", "))
}

func (sb *SyncBuilder) triggerTypes() []string {
	types := []string{polling.TriggerType}
	for _, f := range sb.factories {
		if tf, ok := f.(sync.TriggerFactory); ok {
			types = append(types, tf.Type())
		}
	}
	return types
}

func sourceType(f sync.Factory) string {
	if st, ok := f.(sync.SourceTyper); ok {
		return st.SourceType()
	}
	return f.Type()
}

// schemeFactory finds the fetcher or streaming factory of a provider name.
func (sb *SyncBuilder) schemeFactory(provider string) (sync.SchemeFactory, bool) {
	for _, f := range sb.factories {
		if sf, ok := f.(sync.SchemeFactory); ok && f.Type() == provider {
			return sf, true
		}
	}
	return nil, false
}

// providerNames are the provider names the factories are selected by, in order.
func (sb *SyncBuilder) providerNames() []string {
	var names []string
	for _, f := range sb.factories {
		if _, ok := f.(sync.SchemeFactory); !ok {
			continue
		}
		names = append(names, f.Type())
		if aliased, ok := f.(sync.Aliased); ok {
			for _, alias := range aliased.Aliases() {
				names = append(names, alias.Name)
			}
		}
	}
	return names
}

func quoteAnd(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "'" + n + "'"
	}
	if len(quoted) < 2 {
		return strings.Join(quoted, "")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

type IK8sClientBuilder interface {
	GetK8sClient() (dynamic.Interface, error)
}

type KubernetesClientBuilder struct{}

func (kcb KubernetesClientBuilder) GetK8sClient() (dynamic.Interface, error) {
	clusterConfig, err := k8sClusterConfig()
	if err != nil {
		return nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(clusterConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to create dynamicClient: %w", err)
	}
	return dynamicClient, nil
}

// k8sClusterConfig build K8s connection config based available configurations
func k8sClusterConfig() (*rest.Config, error) {
	cfg := os.Getenv("KUBECONFIG")

	var clusterConfig *rest.Config
	var err error

	if cfg != "" {
		clusterConfig, err = clientcmd.BuildConfigFromFlags("", cfg)
		if err != nil {
			return nil, fmt.Errorf("error building cluster config from flags: %w", err)
		}
	} else {
		clusterConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("error fetching cluster config: %w", err)
		}
	}

	return clusterConfig, nil
}
