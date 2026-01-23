package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zijiren233/sealos-state-metric/pkg/collector"
	"github.com/zijiren233/sealos-state-metric/pkg/config"
	"github.com/zijiren233/sealos-state-metric/pkg/identity"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// slowCollectorThreshold defines the duration after which a collector is considered slow
	slowCollectorThreshold = 500 * time.Millisecond
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
	instance   string // instance identity (pod name or hostname)
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

// InitConfig holds the configuration for initializing collectors
type InitConfig struct {
	//nolint:containedctx // Context passed to collectors for lifecycle management
	Ctx                  context.Context
	RestConfig           *rest.Config
	Client               kubernetes.Interface
	ConfigContent        []byte
	Identity             string
	NodeName             string
	PodName              string
	MetricsNamespace     string
	InformerResyncPeriod time.Duration
	EnabledCollectors    []string
}

// Initialize creates collector instances for the specified collectors.
// It should be called once during application startup.
func (r *Registry) Initialize(cfg *InitConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.collectors) > 0 {
		return errors.New("registry already initialized")
	}

	r.createCollectors(cfg, "Initializing")

	return nil
}

// Reinitialize reinitializes all collectors with new configuration.
// This is used for configuration hot-reloading.
// IMPORTANT: Caller must ensure all collectors are stopped before calling this method.
// This method will clear the collectors map and create new collector instances.
//
// Note: After this method returns, collectors are created but NOT started.
// Caller should immediately call Start/StartNonLeaderCollectors/StartLeaderCollectors
// to start them. To avoid the gap between Reinitialize and Start, consider using
// ReinitializeAndStart instead.
func (r *Registry) Reinitialize(cfg *InitConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Clear collectors map (assumes all collectors are already stopped by caller)
	r.collectors = make(map[string]collector.Collector)

	r.createCollectors(cfg, "Reinitializing")

	return nil
}

// createCollectors creates collector instances from factories
// Must be called with r.mu held
func (r *Registry) createCollectors(cfg *InitConfig, action string) {
	logger := log.WithField("module", "registry")

	// Set instance identity (priority: config > NodeName > PodName > auto-detected)
	r.instance = identity.GetWithConfig(cfg.Identity, cfg.NodeName, cfg.PodName)

	logger.WithFields(log.Fields{
		"enabled":  cfg.EnabledCollectors,
		"instance": r.instance,
	}).Infof("%s collectors", action)

	// Create config loader: content -> env (priority: defaults < content < env)
	configLoader := config.NewWrapConfigLoader()
	if len(cfg.ConfigContent) > 0 {
		configLoader.Add(config.NewModuleConfigLoader(cfg.ConfigContent))
	}

	configLoader.Add(config.NewEnvConfigLoader())

	// Create collectors from factories
	for _, name := range cfg.EnabledCollectors {
		factory, exists := r.factories[name]
		if !exists {
			logger.Warnf("Collector factory not found: %s", name)
			continue
		}

		factoryCtx := &collector.FactoryContext{
			Ctx:                  cfg.Ctx,
			RestConfig:           cfg.RestConfig,
			Client:               cfg.Client,
			ConfigLoader:         configLoader,
			Identity:             r.instance,
			NodeName:             cfg.NodeName,
			PodName:              cfg.PodName,
			MetricsNamespace:     cfg.MetricsNamespace,
			InformerResyncPeriod: cfg.InformerResyncPeriod,
			Logger:               logger.WithField("collector", name),
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
		}).Info("Collector created")
	}
}

// Start starts all registered collectors
func (r *Registry) Start(ctx context.Context) error {
	return r.startCollectors(ctx, nil)
}

// StartNonLeaderCollectors starts only collectors that do NOT require leader election
func (r *Registry) StartNonLeaderCollectors(ctx context.Context) error {
	requireLeader := false
	return r.startCollectors(ctx, &requireLeader)
}

// StartLeaderCollectors starts only collectors that require leader election
func (r *Registry) StartLeaderCollectors(ctx context.Context) error {
	requireLeader := true
	return r.startCollectors(ctx, &requireLeader)
}

// startCollectors starts collectors based on leader election filter.
// - If requireLeader is nil, starts all collectors
// - If requireLeader is false, starts only non-leader collectors
// - If requireLeader is true, starts only leader collectors
func (r *Registry) startCollectors(ctx context.Context, requireLeader *bool) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	logger := log.WithField("module", "registry")

	if len(r.collectors) == 0 {
		logger.Warn("No collectors to start")
		return nil
	}

	var toStart []string
	for name, c := range r.collectors {
		// Filter based on leader election requirement
		if requireLeader == nil {
			// Start all collectors
			toStart = append(toStart, name)
		} else if *requireLeader == c.RequiresLeaderElection() {
			// Start only matching collectors
			toStart = append(toStart, name)
		}
	}

	var filterDesc string
	switch {
	case requireLeader == nil:
		filterDesc = "all"
	case *requireLeader:
		filterDesc = "leader-required"
	default:
		filterDesc = "non-leader"
	}

	logger.WithFields(log.Fields{
		"count":  len(toStart),
		"filter": filterDesc,
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
	r.mu.RLock()
	defer r.mu.RUnlock()

	logger := log.WithField("module", "registry")

	if len(r.collectors) == 0 {
		return nil
	}

	logger.WithField("count", len(r.collectors)).Info("Stopping all collectors")

	var errs []error
	for name, c := range r.collectors {
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

// StopLeaderCollectors stops only collectors that require leader election
func (r *Registry) StopLeaderCollectors() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	logger := log.WithField("module", "registry")

	if len(r.collectors) == 0 {
		return nil
	}

	var toStop []string
	for name, c := range r.collectors {
		if c.RequiresLeaderElection() {
			toStop = append(toStop, name)
		}
	}

	logger.WithField("count", len(toStop)).Info("Stopping leader collectors")

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

// StopNonLeaderCollectors stops only collectors that do not require leader election
func (r *Registry) StopNonLeaderCollectors() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	logger := log.WithField("module", "registry")

	if len(r.collectors) == 0 {
		return nil
	}

	var toStop []string
	for name, c := range r.collectors {
		if !c.RequiresLeaderElection() {
			toStop = append(toStop, name)
		}
	}

	logger.WithFields(log.Fields{
		"count": len(toStop),
	}).Info("Stopping non-leader collectors")

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
