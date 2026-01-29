package domain

import (
	"context"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/labring/sealos-state-metrics/pkg/registry"
)

const collectorName = "domain"

func init() {
	registry.MustRegister(collectorName, NewCollector)
}

// NewCollector creates a new Domain collector
func NewCollector(factoryCtx *collector.FactoryContext) (collector.Collector, error) {
	// 1. Start with hard-coded defaults
	cfg := NewDefaultConfig()

	// 2. Load configuration from ConfigLoader pipe (file -> env)
	// ConfigLoader is never nil and handles priority: defaults < file < env
	if err := factoryCtx.ConfigLoader.LoadModuleConfig("collectors.domain", cfg); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load domain collector config, using defaults")
	}

	c := &Collector{
		BaseCollector: base.NewBaseCollector(
			collectorName,
			factoryCtx.Logger,
			base.WithWaitReadyOnCollect(true),
		),
		config: cfg,
		ips:    make(map[string]*IPHealth),
		logger: factoryCtx.Logger,
	}

	// Create checker
	c.checker = NewDomainChecker(
		cfg.CheckTimeout,
		cfg.IncludeHTTPCheck,
		true, // checkDNS is always true as we need IPs
		cfg.IncludeCertCheck,
	)

	c.initMetrics(factoryCtx.MetricsNamespace)

	// Set lifecycle hooks
	c.SetLifecycle(base.LifecycleFuncs{
		StartFunc: func(ctx context.Context) error {
			// Start polling goroutine
			go c.pollLoop(ctx)

			c.logger.Info("Domain collector started successfully")
			return nil
		},
		StopFunc: func() error {
			return nil
		},
		CollectFunc: c.collect,
	})

	return c, nil
}
