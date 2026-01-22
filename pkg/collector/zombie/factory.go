package zombie

import (
	"fmt"

	"github.com/zijiren233/sealos-state-metric/pkg/collector"
	"github.com/zijiren233/sealos-state-metric/pkg/collector/base"
	"github.com/zijiren233/sealos-state-metric/pkg/registry"
	corev1 "k8s.io/api/core/v1"
	metricsclientset "k8s.io/metrics/pkg/client/clientset/versioned"
)

const collectorName = "zombie"

func init() {
	registry.MustRegister(collectorName, NewCollector)
}

// NewCollector creates a new Zombie collector
func NewCollector(factoryCtx *collector.FactoryContext) (collector.Collector, error) {
	// 1. Start with hard-coded defaults
	cfg := NewDefaultConfig()

	// 2. Load configuration from ConfigLoader pipe (file -> env)
	// ConfigLoader is never nil and handles priority: defaults < file < env
	if err := factoryCtx.ConfigLoader.LoadModuleConfig("collectors.zombie", cfg); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load zombie collector config, using defaults")
	}

	// Create metrics clientset
	metricsClientset, err := metricsclientset.NewForConfig(factoryCtx.RestConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics clientset: %w", err)
	}

	c := &Collector{
		BaseCollector: base.NewBaseCollector(
			collectorName,
			collector.TypePolling,
			factoryCtx.Logger,
		),
		client:           factoryCtx.Client,
		metricsClientset: metricsClientset,
		config:           cfg,
		nodes:            make(map[string]*corev1.Node),
		nodeHasMetrics:   make(map[string]bool),
		stopCh:           make(chan struct{}),
		logger:           factoryCtx.Logger,
	}

	c.initMetrics(factoryCtx.MetricsNamespace)
	c.SetCollectFunc(c.collect)

	return c, nil
}
