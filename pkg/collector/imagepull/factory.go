package imagepull

import (
	"time"

	"github.com/zijiren233/sealos-state-metric/pkg/collector"
	"github.com/zijiren233/sealos-state-metric/pkg/collector/base"
	"github.com/zijiren233/sealos-state-metric/pkg/registry"
)

const collectorName = "imagepull"

func init() {
	registry.MustRegister(collectorName, NewCollector)
}

// NewCollector creates a new ImagePull collector
func NewCollector(factoryCtx *collector.FactoryContext) (collector.Collector, error) {
	// 1. Start with hard-coded defaults
	cfg := NewDefaultConfig()

	// 2. Load configuration from ConfigLoader pipe (file -> env)
	// ConfigLoader is never nil and handles priority: defaults < file < env
	if err := factoryCtx.ConfigLoader.LoadModuleConfig("collectors.imagepull", cfg); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load imagepull collector config, using defaults")
	}

	c := &Collector{
		BaseCollector: base.NewBaseCollector(
			collectorName,
			collector.TypeInformer,
			factoryCtx.Logger,
		),
		client:     factoryCtx.Client,
		config:     cfg,
		classifier: NewFailureClassifier(),
		failures:   make(map[string]*PullFailureInfo),
		slowPulls:  make(map[string]*SlowPullInfo),
		slowTimers: make(map[string]*time.Timer),
		stopCh:     make(chan struct{}),
		logger:     factoryCtx.Logger,
	}

	c.initMetrics(factoryCtx.MetricsNamespace)
	c.SetCollectFunc(c.collect)

	return c, nil
}
