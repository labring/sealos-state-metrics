package imagepull

import (
	"context"
	"errors"
	"time"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/labring/sealos-state-metrics/pkg/registry"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

			// Apply transform to reduce memory usage
			// Only keep necessary fields for image pull monitoring
			_ = c.podInformer.SetTransform(func(obj any) (any, error) {
				pod, ok := obj.(*corev1.Pod)
				if !ok {
					return obj, nil
				}

				// Create a minimal pod object with only required fields
				transformed := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: pod.Namespace,
						Name:      pod.Name,
						// Keep UID for proper object tracking
						UID: pod.UID,
					},
					Spec: corev1.PodSpec{
						// Only keep node name
						NodeName: pod.Spec.NodeName,
					},
					Status: corev1.PodStatus{
						// Only keep container statuses
						InitContainerStatuses: trimContainerStatuses(
							pod.Status.InitContainerStatuses,
						),
						ContainerStatuses: trimContainerStatuses(
							pod.Status.ContainerStatuses,
						),
					},
				}

				return transformed, nil
			})

			//nolint:errcheck // AddEventHandler returns (registration, error) but error is always nil in client-go
			c.podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc:    func(obj any) { c.handlePodAdd(ctx, obj) },
				UpdateFunc: func(oldObj, newObj any) { c.handlePodUpdate(ctx, oldObj, newObj) },
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

// trimContainerStatuses reduces memory by keeping only necessary container status fields
func trimContainerStatuses(statuses []corev1.ContainerStatus) []corev1.ContainerStatus {
	if len(statuses) == 0 {
		return nil
	}

	trimmed := make([]corev1.ContainerStatus, len(statuses))
	for i, cs := range statuses {
		trimmed[i] = corev1.ContainerStatus{
			Name:        cs.Name,
			Image:       cs.Image,
			ContainerID: cs.ContainerID,
			State: corev1.ContainerState{
				Waiting: cs.State.Waiting, // Keep full waiting state (reason, message)
			},
		}
	}

	return trimmed
}
