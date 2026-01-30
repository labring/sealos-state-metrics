package util

import (
	"fmt"
	"os"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// NewKubernetesClient creates a new Kubernetes client
// Returns both the client and the underlying rest.Config
func NewKubernetesClient(
	kubeconfig string,
	qps float32,
	burst int,
) (*rest.Config, kubernetes.Interface, error) {
	config, err := buildConfig(kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build config: %w", err)
	}

	// Set QPS and burst
	config.QPS = qps
	config.Burst = burst

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create client: %w", err)
	}

	return config, client, nil
}

// buildConfig builds a Kubernetes REST config from kubeconfig or in-cluster config
// Priority: kubeconfig parameter > KUBECONFIG env var > in-cluster config
func buildConfig(kubeconfig string) (*rest.Config, error) {
	// 1. If kubeconfig parameter is provided, use it
	if kubeconfig != "" {
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
		}

		return config, nil
	}

	// 2. Try KUBECONFIG environment variable
	if envKubeconfig := os.Getenv("KUBECONFIG"); envKubeconfig != "" {
		config, err := clientcmd.BuildConfigFromFlags("", envKubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to build config from KUBECONFIG env: %w", err)
		}

		return config, nil
	}

	// 3. Fall back to in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf(
			"failed to get in-cluster config: %w (hint: ensure you're running in a Kubernetes cluster or provide valid kubeconfig)",
			err,
		)
	}

	return config, nil
}
