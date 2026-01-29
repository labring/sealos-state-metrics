package cloudbalance

import (
	"context"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/labring/sealos-state-metrics/pkg/registry"
)

const collectorName = "cloudbalance"

func init() {
	registry.MustRegister(collectorName, NewCollector)
}

// NewCollector creates a new CloudBalance collector
func NewCollector(factoryCtx *collector.FactoryContext) (collector.Collector, error) {
	// 1. Start with hard-coded defaults
	cfg := NewDefaultConfig()

	// 2. Load configuration from ConfigLoader pipe (file -> env)
	// ConfigLoader is never nil and handles priority: defaults < file < env
	if err := factoryCtx.ConfigLoader.LoadModuleConfig("collectors.cloudbalance", cfg); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load cloudbalance collector config, using defaults")
	}

	c := &Collector{
		BaseCollector: base.NewBaseCollector(
			collectorName,
			factoryCtx.Logger,
			base.WithWaitReadyOnCollect(true),
		),
		config:   cfg,
		balances: make(map[string]float64),
		logger:   factoryCtx.Logger,
	}

	c.initMetrics(factoryCtx.MetricsNamespace)

	// Set lifecycle hooks
	c.SetLifecycle(base.LifecycleFuncs{
		StartFunc: func(ctx context.Context) error {
			// Start background polling
			go c.pollLoop(ctx)

			c.logger.Info("CloudBalance collector started successfully")
			return nil
		},
		CollectFunc: c.collect,
	})

	return c, nil
}
