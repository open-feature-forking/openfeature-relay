package builder

import (
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/sync/azblob"
	"github.com/open-feature-forking/openfeature-relay/sync/builder/official"
	"github.com/open-feature-forking/openfeature-relay/sync/file"
	flagdgrpc "github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc"
	"github.com/open-feature-forking/openfeature-relay/sync/gcs"
	httpSync "github.com/open-feature-forking/openfeature-relay/sync/http"
	ofoperator "github.com/open-feature-forking/openfeature-relay/sync/openfeature-operator"
	"github.com/open-feature-forking/openfeature-relay/sync/s3"
	"k8s.io/client-go/dynamic"
)

// The builder dispatches to the factories it is given and imports no sync module. Its tests run against the official
// components, which a builder created without factories gets here, with the Kubernetes client of the builder.
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

// clientFunc adapts a function to a client builder.
type clientFunc func() (dynamic.Interface, error)

func (f clientFunc) GetK8sClient() (dynamic.Interface, error) { return f() }

func init() {
	defaultFactories = func(sb *SyncBuilder) []sync.Factory {
		sb.k8sClientBuilder = &ofoperator.KubernetesClientBuilder{}
		return official.With(official.Options{K8sClient: clientFunc(func() (dynamic.Interface, error) {
			return sb.k8sClientBuilder.GetK8sClient()
		})})
	}
}

// Official returns the official sync components.
func Official() []sync.Factory { return official.Factories() }
