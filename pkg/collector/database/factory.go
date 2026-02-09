package database

import (
	"context"
	"fmt"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/labring/sealos-state-metrics/pkg/registry"
)

const collectorName = "database"

func init() {
	// Automatically register to global registry
	registry.MustRegister(collectorName, NewCollector)
}

// NewCollector creates a new Database collector
func NewCollector(factoryCtx *collector.FactoryContext) (collector.Collector, error) {
	// 1. Create default configuration
	cfg := NewDefaultConfig()

	// 2. Load configuration from config loader (file -> env vars)
	if err := factoryCtx.ConfigLoader.LoadModuleConfig("collectors.database", cfg); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load database collector config, using defaults")
	}

	// 3. Get Kubernetes client (required for this collector)
	client, err := factoryCtx.ClientProvider.GetClient()
	if err != nil {
		return nil, fmt.Errorf("kubernetes client is required for database collector: %w", err)
	}

	// 4. Create collector instance
	c := &Collector{
		BaseCollector: base.NewBaseCollector(
			collectorName,
			factoryCtx.Logger,
			base.WithWaitReadyOnCollect(true),
		),
		config:         cfg,
		logger:         factoryCtx.Logger,
		client:         client,
		dbConnectivity: make(map[string]*DatabaseStatus),
	}

	// 5. Initialize Prometheus metrics
	c.initMetrics(factoryCtx.MetricsNamespace)

	// 6. Set up lifecycle hooks
	c.SetLifecycle(base.LifecycleFuncs{
		StartFunc: func(ctx context.Context) error {
			// Start background polling
			go c.pollLoop(ctx)
			c.logger.Info("Database collector started successfully")
			return nil
		},
		CollectFunc: c.collect,
		StopFunc: func() error {
			c.logger.Info("Database collector stopped")
			return nil
		},
	})

	return c, nil
}
