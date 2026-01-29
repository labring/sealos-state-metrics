package lvm

import (
	"context"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/labring/sealos-state-metrics/pkg/registry"
)

const collectorName = "lvm"

func init() {
	registry.MustRegister(collectorName, NewCollector)
}

// NewCollector creates a new LVM collector
func NewCollector(factoryCtx *collector.FactoryContext) (collector.Collector, error) {
	// 1. Start with hard-coded defaults
	cfg := NewDefaultConfig()

	// 2. Load configuration from ConfigLoader pipe (file -> env)
	// ConfigLoader is never nil and handles priority: defaults < file < env
	if err := factoryCtx.ConfigLoader.LoadModuleConfig("collectors.lvm", cfg); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load lvm collector config, using defaults")
	}

	// 3. Use global NodeName if not set in collector config
	if cfg.NodeName == "" {
		cfg.NodeName = factoryCtx.NodeName
	}

	c := &Collector{
		BaseCollector: base.NewBaseCollector(
			collectorName,
			factoryCtx.Logger,
			base.WithLeaderElection(false), // LVM collector runs on each node
			base.WithWaitReadyOnCollect(true),
		),
		config: cfg,
		logger: factoryCtx.Logger,
		stopCh: make(chan struct{}),
	}

	c.initMetrics(factoryCtx.MetricsNamespace)

	// Set lifecycle hooks
	c.SetLifecycle(base.LifecycleFuncs{
		StartFunc: func(ctx context.Context) error {
			// Recreate stop channel to support restart
			c.stopCh = make(chan struct{})

			// Start metrics collection in background
			go c.startMetricsCollection(ctx)

			c.logger.Info("LVM collector started successfully")

			return nil
		},
		StopFunc: func() error {
			close(c.stopCh)
			return nil
		},
		CollectFunc: c.collect,
	})

	return c, nil
}
