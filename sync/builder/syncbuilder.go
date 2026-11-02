package builder

import (
	"fmt"
	"os"
	"regexp"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/polling"
	"github.com/open-feature-forking/openfeature-relay/sync/azblob"
	"github.com/open-feature-forking/openfeature-relay/sync/file"
	"github.com/open-feature-forking/openfeature-relay/sync/gcs"
	"github.com/open-feature-forking/openfeature-relay/sync/grpc"
	"github.com/open-feature-forking/openfeature-relay/sync/grpc/credentials"
	httpSync "github.com/open-feature-forking/openfeature-relay/sync/http"
	"github.com/open-feature-forking/openfeature-relay/sync/kubernetes"
	"github.com/open-feature-forking/openfeature-relay/sync/s3"
	"go.uber.org/zap"
	googlegrpc "google.golang.org/grpc"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	syncProviderFile       = "file"
	syncProviderFsNotify   = "fsnotify"
	syncProviderFileInfo   = "fileinfo"
	syncProviderGrpc       = "grpc"
	syncProviderKubernetes = "kubernetes"
	syncProviderHTTP       = "http"
	syncProviderGcs        = gcs.Provider
	syncProviderAzblob     = azblob.Provider
	syncProviderS3         = s3.Provider
)

var (
	regCrd                *regexp.Regexp
	regURL                *regexp.Regexp
	regGRPC               *regexp.Regexp
	regGRPCSecure         *regexp.Regexp
	regGRPCCustomResolver *regexp.Regexp
	regFile               *regexp.Regexp
)

func init() {
	regCrd = regexp.MustCompile("^core.openfeature.dev/")
	regURL = regexp.MustCompile("^https?://")
	regGRPC = regexp.MustCompile("^" + grpc.Prefix)
	regGRPCSecure = regexp.MustCompile("^" + grpc.PrefixSecure)
	regGRPCCustomResolver = regexp.MustCompile("^" + grpc.SupportedScheme)
	regFile = regexp.MustCompile("^file:")
}

type ISyncBuilder interface {
	SyncFromURI(uri string, logger *logger.Logger) (sync.ISync, error)
	SyncsFromConfig(sourceConfig []sync.SourceConfig, logger *logger.Logger) ([]sync.ISync, error)
}

type SyncBuilder struct {
	k8sClientBuilder IK8sClientBuilder

	// SyncMetricsRecorder, when non-nil, is passed to every sync provider so they can
	// record the universal client-side sync metrics
	SyncMetricsRecorder sync.MetricsRecorder

	// StreamMetricsRecorder, when non-nil, is passed to gRPC sync providers to record their stream lifecycle.
	StreamMetricsRecorder grpc.StreamMetricsRecorder

	// GRPCDialOptions are passed to gRPC sync providers as client connection options.
	GRPCDialOptions []googlegrpc.DialOption
}

func NewSyncBuilder() *SyncBuilder {
	return &SyncBuilder{
		k8sClientBuilder: &KubernetesClientBuilder{},
	}
}

func (sb *SyncBuilder) SyncFromURI(uri string, logger *logger.Logger) (sync.ISync, error) {
	switch uriB := []byte(uri); {
	// filepath may be used for debugging, not recommended in deployment
	case regFile.Match(uriB):
		return sb.newFile(uri, logger), nil
	case regCrd.Match(uriB):
		return sb.newK8s(uri, logger)
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

func (sb *SyncBuilder) syncFromConfig(sourceConfig sync.SourceConfig, logger *logger.Logger) (sync.ISync, error) {
	switch sourceConfig.Provider {
	case syncProviderFile:
		return sb.newFile(sourceConfig.URI, logger), nil
	case syncProviderFsNotify:
		logger.Debug(fmt.Sprintf("using fsnotify sync-provider for: %q", sourceConfig.URI))
		return sb.newFsNotify(sourceConfig.URI, logger), nil
	case syncProviderFileInfo:
		logger.Debug(fmt.Sprintf("using fileinfo sync-provider for: %q", sourceConfig.URI))
		return sb.newFileInfo(sourceConfig.URI, logger), nil
	case syncProviderKubernetes:
		logger.Debug(fmt.Sprintf("using kubernetes sync-provider for: %s", sourceConfig.URI))
		return sb.newK8s(sourceConfig.URI, logger)
	case syncProviderHTTP:
		logger.Debug(fmt.Sprintf("using remote sync-provider for: %s", sourceConfig.URI))
		return sb.newHTTP(sourceConfig, logger)
	case syncProviderGrpc:
		logger.Debug(fmt.Sprintf("using grpc sync-provider for: %s", sourceConfig.URI))
		return sb.newGRPC(sourceConfig, logger), nil
	case syncProviderGcs:
		logger.Debug(fmt.Sprintf("using blob sync-provider with gcs driver for: %s", sourceConfig.URI))
		return gcs.New(sourceConfig, logger, sb.SyncMetricsRecorder)
	case syncProviderAzblob:
		logger.Debug(fmt.Sprintf("using blob sync-provider with azblob driver for: %s", sourceConfig.URI))
		return azblob.New(sourceConfig, logger, sb.SyncMetricsRecorder)
	case syncProviderS3:
		logger.Debug(fmt.Sprintf("using blob sync-provider with s3 driver for: %s", sourceConfig.URI))
		return s3.New(sourceConfig, logger, sb.SyncMetricsRecorder)

	default:
		return nil, fmt.Errorf("invalid sync provider: %s, must be one of with "+
			"'%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s' or '%s'",
			sourceConfig.Provider, syncProviderFile, syncProviderFsNotify, syncProviderFileInfo,
			syncProviderKubernetes, syncProviderHTTP, syncProviderGrpc, syncProviderGcs, syncProviderAzblob, syncProviderS3)
	}
}

// newFile returns an fsinfo sync if we are in k8s or fileinfo if not
func (sb *SyncBuilder) newFile(uri string, logger *logger.Logger) *file.Sync {
	switch os.Getenv("KUBERNETES_SERVICE_HOST") {
	case "":
		// no k8s service host env; use fileinfo
		return sb.newFileInfo(uri, logger)
	default:
		// default to fsnotify
		return sb.newFsNotify(uri, logger)
	}
}

// return a new file.Sync that uses fsnotify under the hood
func (sb *SyncBuilder) newFsNotify(uri string, logger *logger.Logger) *file.Sync {
	s := file.NewFileSync(
		regFile.ReplaceAllString(uri, ""),
		file.FSNOTIFY,
		0, // poll interval is unused by the fsnotify watcher
		logger.WithFields(
			zap.String("component", "sync"),
			zap.String("sync", syncProviderFsNotify),
		),
	)
	s.SyncMetricsRecorder = sb.SyncMetricsRecorder
	return s
}

// return a new file.Sync that uses os.Stat/fs.FileInfo under the hood
func (sb *SyncBuilder) newFileInfo(uri string, logger *logger.Logger) *file.Sync {
	s := file.NewFileSync(
		regFile.ReplaceAllString(uri, ""),
		file.FILEINFO,
		0, // 0 => use the fileinfo watcher's default poll interval
		logger.WithFields(
			zap.String("component", "sync"),
			zap.String("sync", syncProviderFileInfo),
		),
	)
	s.SyncMetricsRecorder = sb.SyncMetricsRecorder
	return s
}

func (sb *SyncBuilder) newK8s(uri string, logger *logger.Logger) (*kubernetes.Sync, error) {
	dynamicClient, err := sb.k8sClientBuilder.GetK8sClient()
	if err != nil {
		return nil, fmt.Errorf("error creating kubernetes clients: %w", err)
	}

	s := kubernetes.NewK8sSync(
		logger.WithFields(
			zap.String("component", "sync"),
			zap.String("sync", "kubernetes"),
		),
		regCrd.ReplaceAllString(uri, ""),
		dynamicClient,
	)
	s.SyncMetricsRecorder = sb.SyncMetricsRecorder
	return s, nil
}

func (sb *SyncBuilder) newGRPC(config sync.SourceConfig, logger *logger.Logger) *grpc.Sync {
	return &grpc.Sync{
		URI: config.URI,
		Logger: logger.WithFields(
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
		SyncMetricsRecorder: sb.SyncMetricsRecorder,

		StreamMetricsRecorder: sb.StreamMetricsRecorder,
		DialOptions:           sb.GRPCDialOptions,
	}
}

// defaultInterval returns the configured interval or 5 seconds if unset,
// along with a validated poller.
func newPoller(config sync.SourceConfig) (uint32, *polling.CronPoller, error) {
	return polling.NewSourcePoller(config.Interval, config.IntervalSeed)
}

func (sb *SyncBuilder) newHTTP(config sync.SourceConfig, logger *logger.Logger) (*httpSync.Sync, error) {
	interval, poller, err := newPoller(config)
	if err != nil {
		return nil, fmt.Errorf("invalid http sync configuration: %w", err)
	}
	s := httpSync.NewHTTP(config, logger, poller, interval)
	s.SetSyncMetricsRecorder(sb.SyncMetricsRecorder)
	return s, nil
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
