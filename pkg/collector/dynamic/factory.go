package dynamic

import (
	"context"
	"errors"
	"fmt"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	"github.com/labring/sealos-state-metrics/pkg/registry"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const collectorName = "dynamic"

func init() {
	registry.MustRegister(collectorName, NewConfigurableDynamicCollector)
}

// NewConfigurableDynamicCollector creates configurable dynamic collectors from config
func NewConfigurableDynamicCollector(
	factoryCtx *collector.FactoryContext,
) (collector.Collector, error) {
	// 1. Load configuration
	cfg := NewDefaultCollectorConfig()
	if err := factoryCtx.ConfigLoader.LoadModuleConfig("collectors.dynamic", cfg); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load dynamic collector config, using defaults")
	}

	// 2. Check if any CRDs configured (no config = disabled)
	if len(cfg.CRDs) == 0 {
		factoryCtx.Logger.Debug("No CRDs configured for dynamic collector, skipping")
		return nil, nil
	}

	// 3. Get Kubernetes rest config (lazy initialization)
	restConfig, err := factoryCtx.GetRestConfig()
	if err != nil {
		return nil, fmt.Errorf("kubernetes rest config is required but not available: %w", err)
	}

	// 4. Create dynamic client
	dynamicClient, err := createDynamicClient(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// 5. Create a multi-collector that manages multiple CRD collectors
	return newMultiCollector(cfg, dynamicClient, factoryCtx)
}

// MultiCollector manages multiple CRD collectors
// Exported for reuse by other collectors
type MultiCollector struct {
	collectors []collector.Collector
	logger     *log.Entry
}

// multiCollector is the internal alias
type multiCollector = MultiCollector

// NewConfigurableCollectorFromConfig creates a single configurable collector from a CRDConfig
// This is a convenience function for creating collectors from configuration
func NewConfigurableCollectorFromConfig(
	name string,
	crdConfig *CRDConfig,
	metricsNamespace string,
	restConfig *rest.Config,
	logger *log.Entry,
) (collector.Collector, error) {
	if crdConfig == nil {
		return nil, errors.New("crdConfig cannot be nil")
	}

	if restConfig == nil {
		return nil, errors.New("rest config cannot be nil")
	}

	// Create dynamic client
	dynamicClient, err := createDynamicClient(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Create configurable collector implementation
	configurableCollector := NewConfigurableCollector(crdConfig, metricsNamespace, logger)

	// Build GVR from config
	gvr := schema.GroupVersionResource{
		Group:    crdConfig.GVR.Group,
		Version:  crdConfig.GVR.Version,
		Resource: crdConfig.GVR.Resource,
	}

	// Create dynamic collector config
	dynamicConfig := &Config{
		GVR:               gvr,
		Namespaces:        crdConfig.Namespaces,
		EventHandler:      configurableCollector.GetEventHandler(),
		MetricsCollector:  configurableCollector.GetMetricsCollector(),
		MetricDescriptors: configurableCollector.GetMetricDescriptors(),
	}

	// Create and return the dynamic collector
	return NewCollector(
		name,
		dynamicClient,
		dynamicConfig,
		logger,
	)
}

// newMultiCollector creates a multi-collector
func newMultiCollector(
	cfg *CollectorConfig,
	dynamicClient dynamic.Interface,
	factoryCtx *collector.FactoryContext,
) (collector.Collector, error) {
	mc := &multiCollector{
		collectors: make([]collector.Collector, 0, len(cfg.CRDs)),
		logger:     factoryCtx.Logger,
	}

	// Create a collector for each CRD
	for i := range cfg.CRDs {
		crdCfg := &cfg.CRDs[i]

		// Validate CRD config
		if crdCfg.Name == "" {
			return nil, fmt.Errorf("CRD config %d: name is required", i)
		}

		if crdCfg.GVR.Resource == "" {
			return nil, fmt.Errorf("CRD config %s: gvr.resource is required", crdCfg.Name)
		}

		// Create collector implementation
		impl := NewConfigurableCollector(crdCfg, factoryCtx.MetricsNamespace, factoryCtx.Logger)

		// Create dynamic collector configuration
		gvr := schema.GroupVersionResource{
			Group:    crdCfg.GVR.Group,
			Version:  crdCfg.GVR.Version,
			Resource: crdCfg.GVR.Resource,
		}

		dynamicCfg := &Config{
			GVR:               gvr,
			Namespaces:        crdCfg.Namespaces,
			EventHandler:      impl.GetEventHandler(),
			MetricsCollector:  impl.GetMetricsCollector(),
			MetricDescriptors: impl.GetMetricDescriptors(),
		}

		// Create dynamic collector
		c, err := NewCollector(
			fmt.Sprintf("%s-%s", collectorName, crdCfg.Name),
			dynamicClient,
			dynamicCfg,
			factoryCtx.Logger.WithField("crd", crdCfg.Name),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create collector for CRD %s: %w", crdCfg.Name, err)
		}

		mc.collectors = append(mc.collectors, c)
	}

	factoryCtx.Logger.WithField("count", len(mc.collectors)).
		Info("Created dynamic collectors")

	return mc, nil
}

// Implement collector.Collector interface for multiCollector

func (mc *multiCollector) Name() string {
	return collectorName
}

func (mc *multiCollector) RequiresLeaderElection() bool {
	return true
}

func (mc *multiCollector) Start(ctx context.Context) error {
	for _, c := range mc.collectors {
		if err := c.Start(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (mc *multiCollector) Stop() error {
	var lastErr error
	for _, c := range mc.collectors {
		if err := c.Stop(); err != nil {
			mc.logger.WithError(err).Warn("Failed to stop collector")
			lastErr = err
		}
	}

	return lastErr
}

func (mc *multiCollector) Health() error {
	for _, c := range mc.collectors {
		if err := c.Health(); err != nil {
			return err
		}
	}

	return nil
}

func (mc *multiCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, c := range mc.collectors {
		c.Describe(ch)
	}
}

func (mc *multiCollector) Collect(ch chan<- prometheus.Metric) {
	for _, c := range mc.collectors {
		c.Collect(ch)
	}
}

// createDynamicClient creates a dynamic Kubernetes client
func createDynamicClient(restConfig *rest.Config) (dynamic.Interface, error) {
	if restConfig == nil {
		return nil, errors.New("rest config cannot be nil")
	}

	client, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return client, nil
}
