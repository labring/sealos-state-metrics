package imagepull

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// PullFailureInfo tracks image pull failure information
type PullFailureInfo struct {
	Namespace string
	Pod       string
	Container string
	Image     string
	Node      string
	Registry  string
	Reason    FailureReason
}

// SlowPullInfo tracks slow image pull information
type SlowPullInfo struct {
	Namespace string
	Pod       string
	Container string
	Image     string
	Node      string
	Registry  string
}

// Collector collects image pull metrics
type Collector struct {
	*base.BaseCollector

	client      kubernetes.Interface
	config      *Config
	podInformer cache.SharedIndexInformer
	classifier  *FailureClassifier
	stopCh      chan struct{}
	logger      *log.Entry

	mu         sync.RWMutex
	failures   map[string]*PullFailureInfo // key: namespace/pod/container
	slowPulls  map[string]*SlowPullInfo    // key: namespace/pod/container
	slowTimers map[string]*time.Timer      // key: namespace/pod/container

	// Metrics
	imagePullFailures *prometheus.Desc
	imagePullSlow     *prometheus.Desc
}

// initMetrics initializes Prometheus metric descriptors
func (c *Collector) initMetrics(namespace string) {
	c.imagePullFailures = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "image", "pull_failures"),
		"Image pull failures",
		[]string{"namespace", "pod", "node", "registry", "image", "reason"},
		nil,
	)
	c.imagePullSlow = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "image", "pull_slow"),
		"Slow image pulls (duration > threshold)",
		[]string{"namespace", "pod", "node", "registry", "image"},
		nil,
	)

	// Register descriptors
	c.MustRegisterDesc(c.imagePullFailures)
	c.MustRegisterDesc(c.imagePullSlow)
}

// HasSynced returns true if the informer has synced
func (c *Collector) HasSynced() bool {
	return c.podInformer != nil && c.podInformer.HasSynced()
}

// handlePodAdd handles pod add events
func (c *Collector) handlePodAdd(ctx context.Context, obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		c.logger.WithField("object", obj).Error("Failed to cast object to Pod")
		return
	}

	c.processPod(ctx, pod)
}

// handlePodUpdate handles pod update events
func (c *Collector) handlePodUpdate(ctx context.Context, _, newObj any) {
	pod, ok := newObj.(*corev1.Pod)
	if !ok {
		c.logger.WithField("object", newObj).Error("Failed to cast object to Pod")
		return
	}

	c.processPod(ctx, pod)
}

// handlePodDelete handles pod delete events
func (c *Collector) handlePodDelete(obj any) {
	// Handle DeletedFinalStateUnknown
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			c.logger.WithField("object", obj).Error("Failed to decode deleted object")
			return
		}

		pod, ok = tombstone.Obj.(*corev1.Pod)
		if !ok {
			c.logger.WithField("object", tombstone.Obj).
				Error("Tombstone contained object that is not a Pod")
			return
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Clean up all failures, slow pulls and timers for this pod
	prefix := pod.Namespace + "/" + pod.Name + "/"
	for key := range c.failures {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			delete(c.failures, key)
		}
	}

	for key := range c.slowPulls {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			delete(c.slowPulls, key)
		}
	}

	for key, timer := range c.slowTimers {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			timer.Stop()
			delete(c.slowTimers, key)
		}
	}
}

// processPod processes a pod to extract image pull information
func (c *Collector) processPod(ctx context.Context, pod *corev1.Pod) {
	// Skip pods not scheduled to any node
	if pod.Spec.NodeName == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	nodeName := pod.Spec.NodeName

	// Process init containers and regular containers
	allStatuses := append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...)
	allStatuses = append(allStatuses, pod.Status.ContainerStatuses...)

	for _, containerStatus := range allStatuses {
		key := pullInfoKey(pod.Namespace, pod.Name, containerStatus.Name)

		// Check for image pull failures
		if containerStatus.State.Waiting != nil &&
			c.isImagePullFailure(containerStatus.State.Waiting.Reason) {
			waiting := containerStatus.State.Waiting
			reason := c.classifier.Classify(waiting.Reason, waiting.Message)
			registry := parseRegistry(containerStatus.Image)

			c.failures[key] = &PullFailureInfo{
				Namespace: pod.Namespace,
				Pod:       pod.Name,
				Container: containerStatus.Name,
				Image:     containerStatus.Image,
				Node:      nodeName,
				Registry:  registry,
				Reason:    reason,
			}

			// Clean up slow pull state if in failure state
			c.cleanupSlowPull(key)

			continue
		}

		// Clean up failure if no longer failing
		delete(c.failures, key)

		// Check for slow pull (container is waiting in ContainerCreating state)
		if containerStatus.ContainerID == "" &&
			containerStatus.State.Waiting != nil &&
			containerStatus.State.Waiting.Reason == "ContainerCreating" {
			c.checkSlowPull(ctx,
				key, pod, containerStatus, nodeName,
			)
		} else {
			// Clean up slow pull state if container started or failed
			c.cleanupSlowPull(key)
		}
	}
}

// isImagePullFailure checks if a reason indicates image pull failure
func (c *Collector) isImagePullFailure(reason string) bool {
	switch reason {
	case "ErrImagePull", "ImagePullBackOff", "Cancelled", "RegistryUnavailable":
		return true
	default:
		return false
	}
}

// checkSlowPull checks if an image pull is slow
func (c *Collector) checkSlowPull(
	ctx context.Context,
	key string,
	pod *corev1.Pod,
	cs corev1.ContainerStatus,
	nodeName string,
) {
	// Check if timer already exists
	if _, exists := c.slowTimers[key]; exists {
		return
	}

	// Create timer for slow pull detection
	timer := time.AfterFunc(c.config.SlowPullThreshold, func() {
		c.handleSlowPullTimer(ctx,
			key, pod.Namespace, pod.Name, cs.Name, cs.Image, nodeName,
		)
	})

	c.slowTimers[key] = timer

	c.logger.WithFields(log.Fields{
		"pod":       pod.Namespace + "/" + pod.Name,
		"container": cs.Name,
		"image":     cs.Image,
	}).Debug("Started slow pull timer")
}

// handleSlowPullTimer is called when slow pull timer fires
func (c *Collector) handleSlowPullTimer(
	ctx context.Context,
	key, namespace, podName, containerName, image, nodeName string,
) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Remove timer
	delete(c.slowTimers, key)

	// Re-check pod status
	pod, err := c.client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		c.logger.WithError(err).WithFields(log.Fields{
			"pod":       namespace + "/" + podName,
			"container": containerName,
		}).Debug("Failed to get pod for slow pull check")

		return
	}

	// Check all container statuses
	allStatuses := append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...)
	allStatuses = append(allStatuses, pod.Status.ContainerStatuses...)

	for _, cs := range allStatuses {
		if cs.Name != containerName {
			continue
		}

		// If still waiting and not in failure state, it's a slow pull
		if cs.ContainerID == "" &&
			cs.State.Waiting != nil &&
			cs.State.Waiting.Reason == "ContainerCreating" {
			registry := parseRegistry(image)

			c.slowPulls[key] = &SlowPullInfo{
				Namespace: namespace,
				Pod:       podName,
				Container: containerName,
				Image:     image,
				Node:      nodeName,
				Registry:  registry,
			}

			c.logger.WithFields(log.Fields{
				"pod":       namespace + "/" + podName,
				"container": containerName,
				"image":     image,
				"node":      nodeName,
			}).Info("Slow image pull detected")
		}

		break
	}
}

// cleanupSlowPull cleans up slow pull state
func (c *Collector) cleanupSlowPull(key string) {
	// Clean up slow pull info
	delete(c.slowPulls, key)

	// Stop and remove timer
	if timer, exists := c.slowTimers[key]; exists {
		timer.Stop()
		delete(c.slowTimers, key)
	}
}

// collect collects metrics
func (c *Collector) collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Collect pull failures
	for _, info := range c.failures {
		ch <- prometheus.MustNewConstMetric(
			c.imagePullFailures,
			prometheus.GaugeValue,
			1,
			info.Namespace,
			info.Pod,
			info.Node,
			info.Registry,
			info.Image,
			string(info.Reason),
		)
	}

	// Collect slow pulls
	for _, info := range c.slowPulls {
		ch <- prometheus.MustNewConstMetric(
			c.imagePullSlow,
			prometheus.GaugeValue,
			1,
			info.Namespace,
			info.Pod,
			info.Node,
			info.Registry,
			info.Image,
		)
	}
}

// pullInfoKey generates a unique key for pull info
func pullInfoKey(namespace, pod, container string) string {
	return namespace + "/" + pod + "/" + container
}

// parseRegistry extracts registry from image name
func parseRegistry(image string) string {
	if image == "" {
		return "unknown"
	}

	parts := strings.Split(image, "/")
	if len(parts) > 1 && strings.Contains(parts[0], ".") {
		return parts[0]
	}

	return "docker.io"
}
