package collector

import (
	"fmt"
	"os"
	"sync"

	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ClientProvider provides lazy initialization of Kubernetes client
// It ensures the client is only initialized once across all collectors
type ClientProvider interface {
	GetRestConfig() (*rest.Config, error)
	GetClient() (kubernetes.Interface, error)
}

// lazyClientProvider implements ClientProvider with lazy initialization using sync.Once
type lazyClientProvider struct {
	config     ClientConfig
	once       sync.Once
	restConfig *rest.Config
	client     kubernetes.Interface
	initErr    error
	logger     *log.Entry
}

// NewClientProvider creates a new ClientProvider
func NewClientProvider(config ClientConfig, logger *log.Entry) ClientProvider {
	return &lazyClientProvider{
		config: config,
		logger: logger,
	}
}

// GetRestConfig returns the Kubernetes REST config, initializing it lazily if needed
func (p *lazyClientProvider) GetRestConfig() (*rest.Config, error) {
	p.once.Do(p.initKubernetesClient)
	return p.restConfig, p.initErr
}

// GetClient returns the Kubernetes client, initializing it lazily if needed
func (p *lazyClientProvider) GetClient() (kubernetes.Interface, error) {
	p.once.Do(p.initKubernetesClient)
	return p.client, p.initErr
}

// initKubernetesClient initializes the Kubernetes client and REST config
// This is called once via sync.Once when GetClient or GetRestConfig is first called
func (p *lazyClientProvider) initKubernetesClient() {
	p.logger.Info("Initializing Kubernetes client (lazy)")

	restConfig, client, err := p.config.newKubernetesClient()
	if err != nil {
		p.initErr = err
		p.logger.WithError(err).Error("Failed to initialize Kubernetes client")
		return
	}

	p.restConfig = restConfig
	p.client = client
	p.logger.Info("Kubernetes client initialized successfully")
}

// newKubernetesClient creates a new Kubernetes client from ClientConfig
func (c *ClientConfig) newKubernetesClient() (*rest.Config, kubernetes.Interface, error) {
	config, err := buildKubernetesConfig(c.Kubeconfig)
	if err != nil {
		return nil, nil, err
	}

	// Set QPS and burst
	config.QPS = c.QPS
	config.Burst = c.Burst

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	return config, client, nil
}

// buildKubernetesConfig builds a Kubernetes REST config from kubeconfig or in-cluster config
// Priority: kubeconfig parameter > KUBECONFIG env var > in-cluster config
func buildKubernetesConfig(kubeconfig string) (*rest.Config, error) {
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
