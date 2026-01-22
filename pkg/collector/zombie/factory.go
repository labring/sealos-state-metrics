package zombie

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zijiren233/sealos-state-metric/pkg/collector"
	"github.com/zijiren233/sealos-state-metric/pkg/collector/base"
	"github.com/zijiren233/sealos-state-metric/pkg/registry"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	metricsclientset "k8s.io/metrics/pkg/client/clientset/versioned"
)

const collectorName = "zombie"

func init() {
	registry.MustRegister(collectorName, NewCollector)
}

// NewCollector creates a new Zombie collector
func NewCollector(factoryCtx *collector.FactoryContext) (collector.Collector, error) {
	// 1. Start with hard-coded defaults
	cfg := NewDefaultConfig()

	// 2. Load configuration from ConfigLoader pipe (file -> env)
	// ConfigLoader is never nil and handles priority: defaults < file < env
	if err := factoryCtx.ConfigLoader.LoadModuleConfig("collectors.zombie", cfg); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load zombie collector config, using defaults")
	}

	// Create metrics clientset
	metricsClientset, err := metricsclientset.NewForConfig(factoryCtx.RestConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics clientset: %w", err)
	}

	c := &Collector{
		BaseCollector: base.NewBaseCollector(
			collectorName,
			collector.TypePolling,
			factoryCtx.Logger,
			base.WithWaitReadyOnCollect(true),
		),
		client:           factoryCtx.Client,
		metricsClientset: metricsClientset,
		config:           cfg,
		nodes:            make(map[string]*corev1.Node),
		nodeHasMetrics:   make(map[string]bool),
		stopCh:           make(chan struct{}),
		logger:           factoryCtx.Logger,
	}

	c.initMetrics(factoryCtx.MetricsNamespace)

	// Set lifecycle hooks
	c.SetLifecycle(base.LifecycleFuncs{
		StartFunc: func(ctx context.Context) error {
			// Recreate stopCh to support restart
			c.stopCh = make(chan struct{})

			// Create informer factory
			factory := informers.NewSharedInformerFactory(c.client, 10*time.Minute)

			// Create node informer
			c.podInformer = factory.Core().V1().Nodes().Informer()
			//nolint:errcheck // AddEventHandler returns (registration, error) but error is always nil in client-go
			c.podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc: func(obj any) {
					node := obj.(*corev1.Node) //nolint:errcheck // Type assertion is safe from informer

					c.mu.Lock()
					c.nodes[node.Name] = node.DeepCopy()
					c.mu.Unlock()

					c.logger.WithField("node", node.Name).Debug("Node added")
				},
				UpdateFunc: func(oldObj, newObj any) {
					node := newObj.(*corev1.Node) //nolint:errcheck // Type assertion is safe from informer

					c.mu.Lock()
					c.nodes[node.Name] = node.DeepCopy()
					c.mu.Unlock()

					c.logger.WithField("node", node.Name).Debug("Node updated")
				},
				DeleteFunc: func(obj any) {
					node := obj.(*corev1.Node) //nolint:errcheck // Type assertion is safe from informer

					c.mu.Lock()
					delete(c.nodes, node.Name)
					delete(c.nodeHasMetrics, node.Name)
					c.mu.Unlock()

					c.logger.WithField("node", node.Name).Debug("Node deleted")
				},
			})

			// Start informer
			factory.Start(c.stopCh)

			// Wait for cache sync
			c.logger.Info("Waiting for zombie collector informer cache sync")

			if !cache.WaitForCacheSync(c.stopCh, c.podInformer.HasSynced) {
				return errors.New("failed to sync zombie collector informer cache")
			}

			// Start polling goroutine
			go c.pollLoop(ctx)

			c.logger.Info("Zombie collector started successfully")

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
