package imagepull

import (
	"context"
	"errors"
	"time"

	"github.com/zijiren233/sealos-state-metric/pkg/collector"
	"github.com/zijiren233/sealos-state-metric/pkg/collector/base"
	"github.com/zijiren233/sealos-state-metric/pkg/registry"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
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
			base.WithWaitReadyOnCollect(true),
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

	// Set lifecycle hooks
	c.SetLifecycle(base.LifecycleFuncs{
		StartFunc: func(ctx context.Context) error {
			// Recreate stopCh to support restart
			c.stopCh = make(chan struct{})

			// Create informer factory
			factory := informers.NewSharedInformerFactory(c.client, 10*time.Minute)

			// Create pod informer
			c.podInformer = factory.Core().V1().Pods().Informer()
			//nolint:errcheck // AddEventHandler returns (registration, error) but error is always nil in client-go
			c.podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc:    func(obj interface{}) { c.handlePodAdd(ctx, obj) },
				UpdateFunc: func(oldObj, newObj interface{}) { c.handlePodUpdate(ctx, oldObj, newObj) },
				DeleteFunc: c.handlePodDelete,
			})

			// Start informers
			factory.Start(c.stopCh)

			// Wait for cache sync
			c.logger.Info("Waiting for imagepull informer cache sync")

			if !cache.WaitForCacheSync(c.stopCh, c.podInformer.HasSynced) {
				return errors.New("failed to sync imagepull informer cache")
			}

			c.logger.Info("ImagePull collector started successfully")

			c.SetReady(true)

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
