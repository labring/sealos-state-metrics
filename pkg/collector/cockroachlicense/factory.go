package cockroachlicense

import (
	"context"
	"fmt"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/labring/sealos-state-metrics/pkg/registry"
)

const collectorName = "cockroachlicense"

func init() {
	registry.MustRegister(collectorName, NewCollector)
}

// NewCollector creates a new Cockroach license collector.
func NewCollector(factoryCtx *collector.FactoryContext) (collector.Collector, error) {
	cfg := NewDefaultConfig()
	if err := factoryCtx.ConfigLoader.LoadModuleConfig("collectors.cockroachlicense", cfg); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load cockroachlicense collector config, using defaults")
	}

	if len(cfg.Clusters) == 0 {
		return nil, fmt.Errorf("at least one CockroachDB cluster must be configured")
	}
	for _, cluster := range cfg.Clusters {
		if cluster.Name == "" {
			return nil, fmt.Errorf("cockroachlicense cluster name cannot be empty")
		}
		if cluster.DSN == "" {
			return nil, fmt.Errorf("cockroachlicense cluster %q DSN cannot be empty", cluster.Name)
		}
	}

	c := &Collector{
		BaseCollector: base.NewBaseCollector(
			collectorName,
			factoryCtx.Logger,
			base.WithWaitReadyOnCollect(true),
		),
		config:   cfg,
		logger:   factoryCtx.Logger,
		clusters: make(map[string]*ClusterStatus),
	}

	c.initMetrics(factoryCtx.MetricsNamespace)

	c.SetLifecycle(base.LifecycleFuncs{
		StartFunc: func(ctx context.Context) error {
			go c.pollLoop(ctx)
			c.logger.Info("Cockroach license collector started successfully")
			return nil
		},
		CollectFunc: c.collect,
		StopFunc: func() error {
			c.logger.Info("Cockroach license collector stopped")
			return nil
		},
		HealthFunc: c.health,
	})

	return c, nil
}
