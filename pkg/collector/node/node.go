package node

import (
	"sync"
	"time"

	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// Collector collects node metrics
type Collector struct {
	*base.BaseCollector

	client   kubernetes.Interface
	config   *Config
	informer cache.SharedIndexInformer
	stopCh   chan struct{}
	logger   *log.Entry

	mu    sync.RWMutex
	nodes map[string]*corev1.Node

	// Metrics
	nodeHealthy   *prometheus.Desc
	nodeCondition *prometheus.Desc
}

// initMetrics initializes Prometheus metric descriptors
func (c *Collector) initMetrics(namespace string) {
	c.nodeHealthy = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "node", "healthy"),
		"Node health status (1=healthy, 0=unhealthy)",
		[]string{"node"},
		nil,
	)
	c.nodeCondition = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "node", "condition"),
		"Node abnormal condition status",
		[]string{"node", "condition", "status"},
		nil,
	)

	// Register descriptors
	c.MustRegisterDesc(c.nodeHealthy)
	c.MustRegisterDesc(c.nodeCondition)
}

// HasSynced returns true if the informer has synced
func (c *Collector) HasSynced() bool {
	return c.informer != nil && c.informer.HasSynced()
}

// collect collects metrics
func (c *Collector) collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	ignoreThreshold := now.Add(-c.config.IgnoreNewNodeDuration)

	for _, node := range c.nodes {
		// Skip new nodes if configured
		if node.CreationTimestamp.After(ignoreThreshold) {
			c.logger.WithFields(log.Fields{
				"node": node.Name,
				"age":  now.Sub(node.CreationTimestamp.Time),
			}).Debug("Skipping new node")

			continue
		}

		// Check if node is healthy
		healthy, abnormalConditions := isNodeHealthy(node)

		if healthy {
			// Node is healthy - only emit node_healthy=1
			ch <- prometheus.MustNewConstMetric(
				c.nodeHealthy,
				prometheus.GaugeValue,
				1,
				node.Name,
			)
		} else {
			// Node is unhealthy - emit node_healthy=0 and all abnormal conditions
			ch <- prometheus.MustNewConstMetric(
				c.nodeHealthy,
				prometheus.GaugeValue,
				0,
				node.Name,
			)

			// Emit abnormal conditions
			for _, condition := range abnormalConditions {
				status := "unknown"
				switch condition.Status {
				case corev1.ConditionTrue:
					status = "true"
				case corev1.ConditionFalse:
					status = "false"
				}

				ch <- prometheus.MustNewConstMetric(
					c.nodeCondition,
					prometheus.GaugeValue,
					boolToFloat64(condition.Status == corev1.ConditionTrue),
					node.Name,
					string(condition.Type),
					status,
				)
			}
		}
	}
}

// isNodeHealthy checks if a node is healthy
// A node is considered healthy if:
// - Ready condition is True
// - All other conditions (MemoryPressure, DiskPressure, PIDPressure, NetworkUnavailable) are False
func isNodeHealthy(node *corev1.Node) (bool, []corev1.NodeCondition) {
	var (
		ready                 = false
		hasMemoryPressure     = false
		hasDiskPressure       = false
		hasPIDPressure        = false
		hasNetworkUnavailable = false
		abnormalConditions    []corev1.NodeCondition
	)

	for _, condition := range node.Status.Conditions {
		switch condition.Type {
		case corev1.NodeReady:
			if condition.Status == corev1.ConditionTrue {
				ready = true
			} else {
				abnormalConditions = append(abnormalConditions, condition)
			}
		case corev1.NodeMemoryPressure:
			if condition.Status == corev1.ConditionTrue {
				hasMemoryPressure = true

				abnormalConditions = append(abnormalConditions, condition)
			}
		case corev1.NodeDiskPressure:
			if condition.Status == corev1.ConditionTrue {
				hasDiskPressure = true

				abnormalConditions = append(abnormalConditions, condition)
			}
		case corev1.NodePIDPressure:
			if condition.Status == corev1.ConditionTrue {
				hasPIDPressure = true

				abnormalConditions = append(abnormalConditions, condition)
			}
		case corev1.NodeNetworkUnavailable:
			if condition.Status == corev1.ConditionTrue {
				hasNetworkUnavailable = true

				abnormalConditions = append(abnormalConditions, condition)
			}
		default:
			// Unknown condition type - treat as abnormal if not False
			if condition.Status != corev1.ConditionFalse {
				abnormalConditions = append(abnormalConditions, condition)
			}
		}
	}

	// Node is healthy only if Ready and no pressure conditions
	healthy := ready &&
		!hasMemoryPressure &&
		!hasDiskPressure &&
		!hasPIDPressure &&
		!hasNetworkUnavailable

	return healthy, abnormalConditions
}

// boolToFloat64 converts a boolean to a float64
func boolToFloat64(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}
