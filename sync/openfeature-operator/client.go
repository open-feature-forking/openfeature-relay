package ofoperator

import (
	"fmt"
	"os"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// KubernetesClientBuilder builds the Kubernetes client from the cluster configuration of the environment: the file
// KUBECONFIG names, or else the in-cluster configuration. It is what the factory uses when it has no builder of its
// own.
type KubernetesClientBuilder struct{}

// GetK8sClient implements ClientBuilder.
func (KubernetesClientBuilder) GetK8sClient() (dynamic.Interface, error) {
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

// k8sClusterConfig builds the cluster connection from the available configuration.
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
