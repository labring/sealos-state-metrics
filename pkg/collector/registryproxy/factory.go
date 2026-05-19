package registryproxy

import (
	"context"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/labring/sealos-state-metrics/pkg/registry"
)

const collectorName = "registryproxy"

func init() {
	registry.MustRegister(collectorName, NewCollector)
}

// NewCollector creates a new RegistryProxy collector.
func NewCollector(factoryCtx *collector.FactoryContext) (collector.Collector, error) {
	cfg := NewDefaultConfig()

	if err := factoryCtx.ConfigLoader.LoadModuleConfig(
		"collectors.registryproxy",
		cfg,
	); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load registryproxy collector config, using defaults")
	}

	runtimeCfg, err := newRuntimeConfig(cfg)
	if err != nil {
		return nil, err
	}

	c := &Collector{
		BaseCollector: base.NewBaseCollector(
			collectorName,
			factoryCtx.Logger,
			base.WithWaitReadyOnCollect(true),
		),
		runtime:    runtimeCfg,
		ips:        make(map[string]*IPHealth),
		registries: make(map[string]*RegistryHealth),
		logger:     factoryCtx.Logger,
	}

	c.checker = NewRegistryChecker(runtimeCfg.checkTimeout)
	c.initMetrics(factoryCtx.MetricsNamespace)

	c.SetLifecycle(base.LifecycleFuncs{
		StartFunc: func(ctx context.Context) error {
			go c.pollLoop(ctx)

			c.logger.Info("RegistryProxy collector started successfully")
			return nil
		},
		StopFunc: func() error {
			return nil
		},
		CollectFunc: c.collect,
	})

	return c, nil
}
