package node

import (
	"github.com/zijiren233/sealos-state-metric/pkg/collector"
	"github.com/zijiren233/sealos-state-metric/pkg/collector/base"
	"github.com/zijiren233/sealos-state-metric/pkg/registry"
	corev1 "k8s.io/api/core/v1"
)

const collectorName = "node"

func init() {
	registry.MustRegister(collectorName, NewCollector)
}

// NewCollector creates a new Node collector
func NewCollector(factoryCtx *collector.FactoryContext) (collector.Collector, error) {
	// 1. Start with hard-coded defaults
	cfg := NewDefaultConfig()

	// 2. Load configuration from ConfigLoader pipe (file -> env)
	// ConfigLoader is never nil and handles priority: defaults < file < env
	if err := factoryCtx.ConfigLoader.LoadModuleConfig("collectors.node", cfg); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load node collector config, using defaults")
	}

	c := &Collector{
		BaseCollector: base.NewBaseCollector(
			collectorName,
			collector.TypeInformer,
			factoryCtx.Logger,
		),
		client: factoryCtx.Client,
		config: cfg,
		nodes:  make(map[string]*corev1.Node),
		stopCh: make(chan struct{}),
		logger: factoryCtx.Logger,
	}

	c.initMetrics(factoryCtx.MetricsNamespace)
	c.SetCollectFunc(c.collect)

	return c, nil
}
