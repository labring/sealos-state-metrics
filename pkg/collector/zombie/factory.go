package zombie

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/labring/sealos-state-metrics/pkg/registry"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	// Get Kubernetes client and rest config (lazy initialization)
	client, err := factoryCtx.GetClient()
	if err != nil {
		return nil, fmt.Errorf("kubernetes client is required but not available: %w", err)
	}

	restConfig, err := factoryCtx.GetRestConfig()
	if err != nil {
		return nil, fmt.Errorf("kubernetes rest config is required but not available: %w", err)
	}

	// 1. Start with hard-coded defaults
	cfg := NewDefaultConfig()

	// 2. Load configuration from ConfigLoader pipe (file -> env)
	// ConfigLoader is never nil and handles priority: defaults < file < env
	if err := factoryCtx.ConfigLoader.LoadModuleConfig("collectors.zombie", cfg); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load zombie collector config, using defaults")
	}

	// Create metrics clientset
	metricsClientset, err := metricsclientset.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics clientset: %w", err)
	}

	c := &Collector{
		BaseCollector: base.NewBaseCollector(
			collectorName,
			factoryCtx.Logger,
			base.WithWaitReadyOnCollect(true),
		),
		client:           client,
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

			// Apply transform to reduce memory usage
			// Only keep necessary fields for zombie node detection
			_ = c.podInformer.SetTransform(func(obj any) (any, error) {
				node, ok := obj.(*corev1.Node)
				if !ok {
					return obj, nil
				}

				// Create a minimal node object with only required fields
				transformed := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: node.Name,
						// Keep UID for proper object tracking
						UID: node.UID,
					},
					Status: corev1.NodeStatus{
						// Only keep conditions for Ready status check
						Conditions: node.Status.Conditions,
					},
				}

				return transformed, nil
			})

			//nolint:errcheck // AddEventHandler returns (registration, error) but error is always nil in client-go
			c.podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
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
