package pod

import (
	"errors"

	"github.com/zijiren233/sealos-state-metric/pkg/collector"
	"github.com/zijiren233/sealos-state-metric/pkg/collector/base"
	"github.com/zijiren233/sealos-state-metric/pkg/registry"
	corev1 "k8s.io/api/core/v1"
)

const collectorName = "pod"

func init() {
	registry.MustRegister(collectorName, NewCollector)
}

// NewCollector creates a new Pod collector
func NewCollector(factoryCtx *collector.FactoryContext) (collector.Collector, error) {
	// 1. Start with hard-coded defaults
	cfg := NewDefaultConfig()

	// 2. Load configuration from ConfigLoader pipe (file -> env)
	// ConfigLoader is never nil and handles priority: defaults < file < env
	if err := factoryCtx.ConfigLoader.LoadModuleConfig("collectors.pod", cfg); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load pod collector config, using defaults")
	}

	if !cfg.Enabled {
		return nil, errors.New("pod collector is not enabled")
	}

	c := &Collector{
		BaseCollector: base.NewBaseCollector(
			collectorName,
			collector.TypeInformer,
			factoryCtx.Logger,
		),
		client: factoryCtx.Client,
		config: cfg,
		pods:   make(map[string]*corev1.Pod),
		stopCh: make(chan struct{}),
		logger: factoryCtx.Logger,
	}

	c.initMetrics(factoryCtx.MetricsNamespace)
	c.SetCollectFunc(c.collect)

	return c, nil
}
