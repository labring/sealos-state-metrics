package collector

import (
	"context"
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Collector is the core interface for all metric collectors.
// It follows the simple and elegant design from node_exporter while
// being tailored for Kubernetes monitoring.
type Collector interface {
	// Name returns the unique identifier of this collector
	Name() string

	// RequiresLeaderElection returns true if this collector should only run on the leader instance.
	// Collectors that only read local data can return false to run on all instances.
	RequiresLeaderElection() bool

	// Start initializes and starts the collector
	Start(ctx context.Context) error

	// Stop gracefully stops the collector
	Stop() error

	// Describe sends the descriptors of each metric to the provided channel.
	// This is part of the prometheus.Collector interface.
	Describe(ch chan<- *prometheus.Desc)

	// Collect sends the current value of each metric to the provided channel.
	// This is part of the prometheus.Collector interface.
	Collect(ch chan<- prometheus.Metric)

	// Health performs a health check on the collector
	Health() error
}

// InformerCollector extends Collector for informer-based collectors
type InformerCollector interface {
	Collector

	// HasSynced returns true if the informer has completed its initial sync
	HasSynced() bool
}

// PollingCollector extends Collector for polling-based collectors
type PollingCollector interface {
	Collector

	// Interval returns the polling interval
	Interval() time.Duration

	// Poll executes one polling cycle
	Poll(ctx context.Context) error
}

// ConfigLoader defines the interface for loading module-specific configuration
type ConfigLoader interface {
	LoadModuleConfig(moduleKey string, target any) error
}

// FactoryContext contains context needed for creating collectors
// Each collector is responsible for loading its own configuration
type FactoryContext struct {
	//nolint:containedctx // Context is part of factory parameters struct, passed to factory functions
	Ctx          context.Context
	ConfigLoader ConfigLoader // Loader for module-specific configuration (never nil, use NullLoader as fallback)

	// Global configs that all collectors might need
	Identity             string // Instance identity (defaults to NodeName > PodName > auto-detected)
	NodeName             string // Node name for node-level collectors (from NODE_NAME env var)
	PodName              string // Pod name (from POD_NAME env var)
	MetricsNamespace     string
	InformerResyncPeriod time.Duration

	// Logger is the base logger, collectors should use Logger.WithField("collector", name) for component-specific logging
	Logger *log.Entry

	// ClientProvider for lazy Kubernetes client initialization (shared across all collectors)
	ClientProvider ClientProvider
}

// ClientConfig holds Kubernetes client configuration
type ClientConfig struct {
	Kubeconfig string
	QPS        float32
	Burst      int
}

// GetRestConfig returns the Kubernetes REST config, initializing it lazily if needed
func (f *FactoryContext) GetRestConfig() (*rest.Config, error) {
	if f.ClientProvider == nil {
		return nil, errors.New("client provider not set")
	}
	return f.ClientProvider.GetRestConfig()
}

// GetClient returns the Kubernetes client, initializing it lazily if needed
func (f *FactoryContext) GetClient() (kubernetes.Interface, error) {
	if f.ClientProvider == nil {
		return nil, errors.New("client provider not set")
	}
	return f.ClientProvider.GetClient()
}

// Factory is a function type that creates a new collector instance.
// This is used in the registration pattern inspired by node_exporter.
// Each collector loads its own configuration independently.
type Factory func(ctx *FactoryContext) (Collector, error)
