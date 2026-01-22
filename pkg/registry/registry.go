package registry

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
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
	MetricsNamespace     string
	InformerResyncPeriod time.Duration
	EnabledCollectors    []string
	Identity             string
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
func (r *Registry) Reinitialize(cfg *InitConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	logger := log.WithField("module", "registry")

	// Stop all existing collectors
	for name, c := range r.collectors {
		if err := c.Stop(); err != nil {
			logger.WithError(err).WithField("name", name).
				Error("Failed to stop collector during reinitialization")
		}
	}

	// Clear collectors map
	r.collectors = make(map[string]collector.Collector)

	r.createCollectors(cfg, "Reinitializing")

	return nil
}

// createCollectors creates collector instances from factories
// Must be called with r.mu held
func (r *Registry) createCollectors(cfg *InitConfig, action string) {
	logger := log.WithField("module", "registry")

	// Set instance identity
	r.instance = identity.GetWithConfig(cfg.Identity)

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

// collectorResult holds the result of a collector execution
type collectorResult struct {
	name     string
	duration time.Duration
	success  bool
}

// PrometheusCollector wraps the registry as a prometheus.Collector.
// This allows all collectors to be registered with a single Prometheus registry.
type PrometheusCollector struct {
	registry *Registry

	// Duration metrics
	collectorDuration *prometheus.Desc
	collectorSuccess  *prometheus.Desc
}

// NewPrometheusCollector creates a new PrometheusCollector
func NewPrometheusCollector(registry *Registry) *PrometheusCollector {
	return &PrometheusCollector{
		registry: registry,
		collectorDuration: prometheus.NewDesc(
			"sealos_state_metric_collector_duration_seconds",
			"Duration of collector scrape in seconds",
			[]string{"collector", "instance"},
			nil,
		),
		collectorSuccess: prometheus.NewDesc(
			"sealos_state_metric_collector_success",
			"Whether collector scrape was successful (1=success, 0=failure)",
			[]string{"collector", "instance"},
			nil,
		),
	}
}

// getInstance returns the current instance identity from the registry
// This is called on each collect to ensure the instance is always up-to-date after reloads
func (pc *PrometheusCollector) getInstance() string {
	pc.registry.mu.RLock()
	defer pc.registry.mu.RUnlock()
	return pc.registry.instance
}

// Describe implements prometheus.Collector
func (pc *PrometheusCollector) Describe(ch chan<- *prometheus.Desc) {
	pc.registry.mu.RLock()

	collectors := make([]collector.Collector, 0, len(pc.registry.collectors))
	for _, c := range pc.registry.collectors {
		collectors = append(collectors, c)
	}

	pc.registry.mu.RUnlock()

	// Describe our own metrics
	ch <- pc.collectorDuration

	ch <- pc.collectorSuccess

	// Describe all collectors concurrently
	var wg sync.WaitGroup
	for _, c := range collectors {
		wg.Add(1)

		go func(col collector.Collector) {
			defer wg.Done()

			col.Describe(ch)
		}(c)
	}

	wg.Wait()
}

// collectFromCollector executes a single collector and returns the result
func collectFromCollector(
	name string,
	col collector.Collector,
	ch chan<- prometheus.Metric,
	logger *log.Entry,
) collectorResult {
	start := time.Now()
	success := true

	// Collect metrics with panic recovery
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.WithFields(log.Fields{
					"collector": name,
					"panic":     r,
				}).Error("Collector panicked during collection")

				success = false
			}
		}()

		col.Collect(ch)
	}()

	duration := time.Since(start)

	// Log slow collectors
	if duration > slowCollectorThreshold {
		logger.WithFields(log.Fields{
			"collector": name,
			"duration":  duration,
		}).Warn("Slow collector detected")
	}

	return collectorResult{
		name:     name,
		duration: duration,
		success:  success,
	}
}

// emitCollectorMetrics emits duration and success metrics for collectors
func (pc *PrometheusCollector) emitCollectorMetrics(
	results []collectorResult,
	ch chan<- prometheus.Metric,
) {
	instance := pc.getInstance()
	for _, result := range results {
		ch <- prometheus.MustNewConstMetric(
			pc.collectorDuration,
			prometheus.GaugeValue,
			result.duration.Seconds(),
			result.name,
			instance,
		)

		successValue := 0.0
		if result.success {
			successValue = 1.0
		}

		ch <- prometheus.MustNewConstMetric(
			pc.collectorSuccess,
			prometheus.GaugeValue,
			successValue,
			result.name,
			instance,
		)
	}
}

// Collect implements prometheus.Collector
func (pc *PrometheusCollector) Collect(ch chan<- prometheus.Metric) {
	// Copy collectors map and instance to reduce lock contention
	pc.registry.mu.RLock()
	collectors := maps.Clone(pc.registry.collectors)
	instance := pc.registry.instance
	pc.registry.mu.RUnlock()

	logger := log.WithField("module", "registry")

	// Setup metric wrapper if instance is configured
	metricCh := ch
	var wrapperWg sync.WaitGroup
	if instance != "" {
		wrapperCh := make(chan prometheus.Metric, 100)
		metricCh = wrapperCh

		wrapperWg.Go(func() {
			wrapMetricsWithInstance(wrapperCh, ch, instance)
		})
	}

	// Collect from all collectors concurrently
	var collectWg sync.WaitGroup
	resultCh := make(chan collectorResult, len(collectors))

	for name, c := range collectors {
		collectWg.Go(func() {
			result := collectFromCollector(name, c, metricCh, logger)
			resultCh <- result
		})
	}

	// Wait for all collectors to finish
	collectWg.Wait()
	close(resultCh)

	// If wrapper is running, close wrapper channel and wait for it
	if instance != "" {
		close(metricCh)
		wrapperWg.Wait()
	}

	// Collect results and emit performance metrics
	results := make([]collectorResult, 0, len(collectors))
	for result := range resultCh {
		results = append(results, result)
	}

	pc.emitCollectorMetrics(results, ch)
}

// wrapMetricsWithInstance wraps metrics by adding instance label
func wrapMetricsWithInstance(
	source <-chan prometheus.Metric,
	dest chan<- prometheus.Metric,
	instance string,
) {
	for metric := range source {
		wrappedMetric := &metricWithInstance{
			Metric:   metric,
			instance: instance,
		}
		dest <- wrappedMetric
	}
}

// metricWithInstance wraps a prometheus.Metric and adds instance label
type metricWithInstance struct {
	prometheus.Metric
	instance string
}

// Write implements prometheus.Metric by adding instance label
func (m *metricWithInstance) Write(out *dto.Metric) error {
	// First, write the original metric
	if err := m.Metric.Write(out); err != nil {
		return err
	}

	// Add instance label
	out.Label = append(out.Label, &dto.LabelPair{
		Name:  stringPtr("instance"),
		Value: stringPtr(m.instance),
	})

	return nil
}

// stringPtr returns a pointer to the given string
func stringPtr(s string) *string {
	return &s
}
