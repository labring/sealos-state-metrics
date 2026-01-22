package domain

import (
	"github.com/zijiren233/sealos-state-metric/pkg/collector"
	"github.com/zijiren233/sealos-state-metric/pkg/collector/base"
	"github.com/zijiren233/sealos-state-metric/pkg/registry"
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
			collector.TypePolling,
			factoryCtx.Logger,
		),
		config: cfg,
		ips:    make(map[string]*IPHealth),
		stopCh: make(chan struct{}),
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
	c.SetCollectFunc(c.collect)

	return c, nil
}
