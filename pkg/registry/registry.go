package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"github.com/zijiren233/sealos-state-metric/pkg/collector"
	"github.com/zijiren233/sealos-state-metric/pkg/config"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	// globalRegistry is the singleton instance of the collector registry
	globalRegistry *Registry
	once           sync.Once
)

// Registry manages the lifecycle of all collectors.
// It follows the node_exporter pattern with a factory-based registration system.
type Registry struct {
	mu         sync.RWMutex
	factories  map[string]collector.Factory
	collectors map[string]collector.Collector
}

// GetRegistry returns the singleton registry instance
func GetRegistry() *Registry {
	once.Do(func() {
		globalRegistry = &Registry{
			factories:  make(map[string]collector.Factory),
			collectors: make(map[string]collector.Collector),
		}
	})

	return globalRegistry
}

// Register registers a collector factory with the given name.
// This function is typically called from init() functions in collector packages.
func Register(name string, factory collector.Factory) {
	registry := GetRegistry()

	registry.mu.Lock()
	defer registry.mu.Unlock()

	logger := log.WithField("module", "registry")

	if _, exists := registry.factories[name]; exists {
		logger.Warnf("Collector factory %s already registered, overwriting", name)
	}

	registry.factories[name] = factory
	logger.WithField("name", name).Debug("Collector factory registered")
}

// MustRegister is like Register but panics if registration fails
func MustRegister(name string, factory collector.Factory) {
	if name == "" {
		panic("collector name cannot be empty")
	}

	if factory == nil {
		panic(fmt.Sprintf("collector factory for %s cannot be nil", name))
	}

	Register(name, factory)
}

// Initialize creates collector instances for the specified collectors.
// It should be called once during application startup.
func (r *Registry) Initialize(
	ctx context.Context,
	restConfig *rest.Config,
	client kubernetes.Interface,
	configContent []byte,
	metricsNamespace string,
	informerResyncPeriod time.Duration,
	enabledCollectors []string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.collectors) > 0 {
		return errors.New("registry already initialized")
	}

	logger := log.WithField("module", "registry")
	logger.WithField("enabled", enabledCollectors).Info("Initializing collectors")

	// Create module config loader with pipe mode: content -> env
	// ConfigLoader is never nil - use NullLoader as fallback
	// The pipe ensures configuration priority: defaults < content < env variables
	configLoader := config.NewWrapConfigLoader()

	// Add content loader if config content is provided
	if len(configContent) > 0 {
		configLoader.Add(config.NewModuleConfigLoader(configContent))
	}

	// Always add env loader (highest priority)
	configLoader.Add(config.NewEnvConfigLoader())

	// Create base logger
	baseLogger := log.WithField("module", "registry")

	for _, name := range enabledCollectors {
		factory, exists := r.factories[name]
		if !exists {
			logger.Warnf("Collector factory not found: %s", name)
			continue
		}

		// Create collector-specific logger with collector name field
		collectorLogger := baseLogger.WithField("collector", name)

		// Create FactoryContext with collector-specific logger
		factoryCtx := &collector.FactoryContext{
			Ctx:                  ctx,
			RestConfig:           restConfig,
			Client:               client,
			ConfigLoader:         configLoader,
			MetricsNamespace:     metricsNamespace,
			InformerResyncPeriod: informerResyncPeriod,
			Logger:               collectorLogger,
		}

		c, err := factory(factoryCtx)
		if err != nil {
			logger.WithField("name", name).WithError(err).Debug("Collector not enabled")
			continue
		}

		r.collectors[name] = c
		logger.WithFields(log.Fields{
			"name": name,
			"type": c.Type(),
		}).Info("Collector initialized")
	}

	return nil
}

// Start starts all registered collectors
func (r *Registry) Start(ctx context.Context) error {
	return r.StartWithLeaderElection(ctx, false)
}

// StartWithLeaderElection starts collectors based on leader election requirement.
// If leaderOnly is true, only starts collectors that require leader election.
// If leaderOnly is false, starts all collectors.
func (r *Registry) StartWithLeaderElection(ctx context.Context, leaderOnly bool) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	logger := log.WithField("module", "registry")

	if len(r.collectors) == 0 {
		logger.Warn("No collectors to start")
		return nil
	}

	var toStart []string
	for name, c := range r.collectors {
		if !leaderOnly || c.RequiresLeaderElection() {
			toStart = append(toStart, name)
		}
	}

	logger.WithFields(log.Fields{
		"count":      len(toStart),
		"leaderOnly": leaderOnly,
	}).Info("Starting collectors")

	var errs []error
	for _, name := range toStart {
		c := r.collectors[name]
		if err := c.Start(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to start collector %s: %w", name, err))
			logger.WithError(err).WithField("name", name).Error("Failed to start collector")
		} else {
			logger.WithFields(log.Fields{
				"name":                   name,
				"requiresLeaderElection": c.RequiresLeaderElection(),
			}).Info("Collector started")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to start %d collector(s): %v", len(errs), errs)
	}

	return nil
}

// Stop stops all registered collectors
func (r *Registry) Stop() error {
	return r.StopWithLeaderElection(false)
}

// StopWithLeaderElection stops collectors based on leader election requirement.
// If leaderOnly is true, only stops collectors that require leader election.
// If leaderOnly is false, stops all collectors.
func (r *Registry) StopWithLeaderElection(leaderOnly bool) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	logger := log.WithField("module", "registry")

	if len(r.collectors) == 0 {
		return nil
	}

	var toStop []string
	for name, c := range r.collectors {
		if !leaderOnly || c.RequiresLeaderElection() {
			toStop = append(toStop, name)
		}
	}

	logger.WithFields(log.Fields{
		"count":      len(toStop),
		"leaderOnly": leaderOnly,
	}).Info("Stopping collectors")

	var errs []error
	for _, name := range toStop {
		c := r.collectors[name]
		if err := c.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop collector %s: %w", name, err))
			logger.WithError(err).WithField("name", name).Error("Failed to stop collector")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to stop %d collector(s): %v", len(errs), errs)
	}

	return nil
}

// GetCollector returns a collector by name
func (r *Registry) GetCollector(name string) (collector.Collector, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	c, exists := r.collectors[name]

	return c, exists
}

// ListCollectors returns the names of all registered collectors
func (r *Registry) ListCollectors() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.collectors))
	for name := range r.collectors {
		names = append(names, name)
	}

	return names
}

// HealthCheck performs health checks on all collectors
func (r *Registry) HealthCheck() map[string]error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	results := make(map[string]error)
	for name, c := range r.collectors {
		results[name] = c.Health()
	}

	return results
}

// PrometheusCollector wraps the registry as a prometheus.Collector.
// This allows all collectors to be registered with a single Prometheus registry.
type PrometheusCollector struct {
	registry *Registry
}

// NewPrometheusCollector creates a new PrometheusCollector
func NewPrometheusCollector(registry *Registry) *PrometheusCollector {
	return &PrometheusCollector{
		registry: registry,
	}
}

// Describe implements prometheus.Collector
func (pc *PrometheusCollector) Describe(ch chan<- *prometheus.Desc) {
	pc.registry.mu.RLock()
	defer pc.registry.mu.RUnlock()

	for _, c := range pc.registry.collectors {
		c.Describe(ch)
	}
}

// Collect implements prometheus.Collector
func (pc *PrometheusCollector) Collect(ch chan<- prometheus.Metric) {
	pc.registry.mu.RLock()
	defer pc.registry.mu.RUnlock()

	for _, c := range pc.registry.collectors {
		c.Collect(ch)
	}
}
