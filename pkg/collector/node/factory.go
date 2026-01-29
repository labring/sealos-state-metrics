package node

import (
	"context"
	"errors"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/labring/sealos-state-metrics/pkg/registry"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

const collectorName = "node"

func init() {
	registry.MustRegister(collectorName, NewCollector)
}

// NewCollector creates a new Node collector
func NewCollector(factoryCtx *collector.FactoryContext) (collector.Collector, error) {
	// 1. Start with hard-coded defaults
	cfg := NewDefaultConfig()

	// 2. Load configuration from ConfigLoader pipe (file -> env)
	// ConfigLoader is never nil and handles priority: defaults < file < env
	if err := factoryCtx.ConfigLoader.LoadModuleConfig("collectors.node", cfg); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load node collector config, using defaults")
	}

	c := &Collector{
		BaseCollector: base.NewBaseCollector(
			collectorName,
			factoryCtx.Logger,
			base.WithWaitReadyOnCollect(true),
		),
		client: factoryCtx.Client,
		config: cfg,
		nodes:  make(map[string]*corev1.Node),
		stopCh: make(chan struct{}),
		logger: factoryCtx.Logger,
	}

	c.initMetrics(factoryCtx.MetricsNamespace)

	// Set lifecycle hooks
	c.SetLifecycle(base.LifecycleFuncs{
		StartFunc: func(ctx context.Context) error {
			// Recreate stopCh to support restart
			c.stopCh = make(chan struct{})

			// Create informer factory
			factory := informers.NewSharedInformerFactory(c.client, c.config.IgnoreNewNodeDuration)

			// Create node informer
			c.informer = factory.Core().V1().Nodes().Informer()

			// Apply transform to reduce memory usage
			// Only keep necessary fields for node health monitoring
			_ = c.informer.SetTransform(func(obj any) (any, error) {
				node, ok := obj.(*corev1.Node)
				if !ok {
					return obj, nil
				}

				// Create a minimal node object with only required fields
				transformed := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:              node.Name,
						CreationTimestamp: node.CreationTimestamp,
						// Keep UID for proper object tracking
						UID: node.UID,
					},
					Status: corev1.NodeStatus{
						// Only keep conditions
						Conditions: node.Status.Conditions,
					},
				}

				return transformed, nil
			})

			// Add event handlers
			//nolint:errcheck // AddEventHandler returns (registration, error) but error is always nil in client-go
			c.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc: func(obj any) {
					node, ok := obj.(*corev1.Node)
					if !ok {
						c.logger.WithField("object", obj).Error("Failed to cast object to Node")
						return
					}

					c.mu.Lock()
					c.nodes[node.Name] = node.DeepCopy()
					c.mu.Unlock()
					c.logger.WithField("node", node.Name).Debug("Node added")
				},
				UpdateFunc: func(oldObj, newObj any) {
					node, ok := newObj.(*corev1.Node)
					if !ok {
						c.logger.WithField("object", newObj).Error("Failed to cast object to Node")
						return
					}

					c.mu.Lock()
					c.nodes[node.Name] = node.DeepCopy()
					c.mu.Unlock()
					c.logger.WithField("node", node.Name).Debug("Node updated")
				},
				DeleteFunc: func(obj any) {
					// Handle DeletedFinalStateUnknown
					node, ok := obj.(*corev1.Node)
					if !ok {
						tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
						if !ok {
							c.logger.WithField("object", obj).
								Error("Failed to decode deleted object")
							return
						}

						node, ok = tombstone.Obj.(*corev1.Node)
						if !ok {
							c.logger.WithField("object", tombstone.Obj).
								Error("Tombstone contained object that is not a Node")
							return
						}
					}

					c.mu.Lock()
					delete(c.nodes, node.Name)
					c.mu.Unlock()
					c.logger.WithField("node", node.Name).Debug("Node deleted")
				},
			})

			// Start informer
			factory.Start(c.stopCh)

			// Wait for cache sync
			c.logger.Info("Waiting for node informer cache sync")

			if !cache.WaitForCacheSync(c.stopCh, c.informer.HasSynced) {
				return errors.New("failed to sync node informer cache")
			}

			c.logger.Info("Node collector started successfully")

			c.SetReady()

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
