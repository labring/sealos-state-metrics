package zombie

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"github.com/zijiren233/sealos-state-metric/pkg/collector/base"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	metricsclientset "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Collector collects zombie node metrics
type Collector struct {
	*base.BaseCollector

	client           kubernetes.Interface
	metricsClientset *metricsclientset.Clientset
	config           *Config
	podInformer      cache.SharedIndexInformer
	stopCh           chan struct{}
	logger           *log.Entry

	mu             sync.RWMutex
	nodes          map[string]*corev1.Node // key: node name
	nodeHasMetrics map[string]bool         // key: node name, value: has metrics

	// Metrics
	nodeKubeletMetricsAvailable *prometheus.Desc
}

// initMetrics initializes Prometheus metric descriptors
func (c *Collector) initMetrics(namespace string) {
	c.nodeKubeletMetricsAvailable = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "node", "kubelet_metrics_available"),
		"Whether kubelet metrics are available for the node (1=available, 0=unavailable)",
		[]string{"node"},
		nil,
	)

	// Register descriptors
	c.MustRegisterDesc(c.nodeKubeletMetricsAvailable)
}

// Start starts the collector
func (c *Collector) Start(ctx context.Context) error {
	if err := c.BaseCollector.Start(ctx); err != nil {
		return err
	}

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
	go c.pollLoop()

	c.logger.Info("Zombie collector started successfully")

	return nil
}

// Stop stops the collector
func (c *Collector) Stop() error {
	close(c.stopCh)
	return c.BaseCollector.Stop()
}

// HasSynced returns true if the informer has synced
func (c *Collector) HasSynced() bool {
	return c.podInformer != nil && c.podInformer.HasSynced()
}

// Interval returns the polling interval
func (c *Collector) Interval() time.Duration {
	return c.config.CheckInterval
}

// Poll performs one check cycle
func (c *Collector) Poll(ctx context.Context) error {
	// Get node metrics from metrics-server
	nodeMetrics, err := c.metricsClientset.MetricsV1beta1().
		NodeMetricses().
		List(ctx, metav1.ListOptions{})
	if err != nil {
		c.logger.WithError(err).Error("Failed to get node metrics")
		return err
	}

	// Create metrics map
	metricsMap := make(map[string]bool)
	if nodeMetrics != nil {
		for _, item := range nodeMetrics.Items {
			metricsMap[item.Name] = true
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Update metrics availability for each Ready node
	for nodeName, node := range c.nodes {
		// Only check Ready nodes
		if !isNodeReady(node) {
			c.logger.WithField("node", nodeName).Debug("Node is not ready, skipping")
			continue
		}

		hasMetrics := metricsMap[nodeName]
		c.nodeHasMetrics[nodeName] = hasMetrics

		if hasMetrics {
			c.logger.WithField("node", nodeName).Debug("Node has metrics")
		} else {
			c.logger.WithField("node", nodeName).Warn("Node missing kubelet metrics")
		}
	}

	return nil
}

// pollLoop runs the polling loop
func (c *Collector) pollLoop() {
	ticker := time.NewTicker(c.config.CheckInterval)
	defer ticker.Stop()

	// Do initial check
	_ = c.Poll(c.Context())

	// Mark as ready after first poll completes
	c.SetReady(true)

	for {
		select {
		case <-ticker.C:
			_ = c.Poll(c.Context())
		case <-c.stopCh:
			return
		}
	}
}

// collect collects metrics
func (c *Collector) collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for nodeName, node := range c.nodes {
		// Only report metrics for Ready nodes
		if !isNodeReady(node) {
			continue
		}

		// Check if node has metrics
		hasMetrics, exists := c.nodeHasMetrics[nodeName]
		if !exists {
			// Not yet polled, skip
			continue
		}

		// Emit kubelet_metrics_available
		ch <- prometheus.MustNewConstMetric(
			c.nodeKubeletMetricsAvailable,
			prometheus.GaugeValue,
			boolToFloat64(hasMetrics),
			nodeName,
		)
	}
}

// isNodeReady checks if node is in Ready status
func isNodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}

	return false
}

// boolToFloat64 converts a boolean to a float64
func boolToFloat64(b bool) float64 {
	if b {
		return 1.0
	}

	return 0.0
}
