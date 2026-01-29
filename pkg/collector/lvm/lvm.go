package lvm

import (
	"context"
	"sync"
	"time"

	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/labring/sealos-state-metrics/pkg/lvm"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Collector collects LVM metrics
type Collector struct {
	*base.BaseCollector

	config *Config
	logger *log.Entry

	mu     sync.RWMutex
	stopCh chan struct{}

	// Metrics descriptors
	lvmVgsTotalCapacity *prometheus.Desc
	lvmVgsTotalFree     *prometheus.Desc

	// Current metric values
	totalCapacity float64
	totalFree     float64
}

// initMetrics initializes Prometheus metric descriptors
func (c *Collector) initMetrics(namespace string) {
	c.lvmVgsTotalCapacity = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "lvm", "vgs_total_capacity"),
		"Total capacity of all volume groups in bytes",
		[]string{"node"},
		nil,
	)
	c.lvmVgsTotalFree = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "lvm", "vgs_total_free"),
		"Total free space of all volume groups in bytes",
		[]string{"node"},
		nil,
	)

	// Register descriptors
	c.MustRegisterDesc(c.lvmVgsTotalCapacity)
	c.MustRegisterDesc(c.lvmVgsTotalFree)
}

// updateMetrics updates LVM metrics by querying the system
func (c *Collector) updateMetrics() {
	vgs, err := lvm.ListLVMVolumeGroup(false)
	if err != nil {
		c.logger.WithError(err).Error("Failed to list LVM volume groups")
		return
	}

	if len(vgs) == 0 {
		c.logger.Debug("No LVM volume groups found")
		return
	}

	vgAmountTotal := resource.NewQuantity(0, resource.BinarySI)

	vgFreeTotal := resource.NewQuantity(0, resource.BinarySI)
	for _, vg := range vgs {
		vgAmountTotal.Add(vg.Size)
		vgFreeTotal.Add(vg.Free)
	}

	c.mu.Lock()
	c.totalCapacity = float64(vgAmountTotal.Value())
	c.totalFree = float64(vgFreeTotal.Value())
	c.mu.Unlock()

	c.logger.WithFields(log.Fields{
		"total_capacity": vgAmountTotal.String(),
		"total_free":     vgFreeTotal.String(),
	}).Debug("Updated LVM metrics")
}

// collect collects metrics
func (c *Collector) collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.config.NodeName != "" {
		ch <- prometheus.MustNewConstMetric(
			c.lvmVgsTotalCapacity,
			prometheus.GaugeValue,
			c.totalCapacity,
			c.config.NodeName,
		)

		ch <- prometheus.MustNewConstMetric(
			c.lvmVgsTotalFree,
			prometheus.GaugeValue,
			c.totalFree,
			c.config.NodeName,
		)
	}
}

// startMetricsCollection starts the metrics collection loop
func (c *Collector) startMetricsCollection(ctx context.Context) {
	ticker := time.NewTicker(c.config.UpdateInterval)
	defer ticker.Stop()

	// Update metrics immediately on start
	c.updateMetrics()

	// Mark as ready after first update
	c.SetReady()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.updateMetrics()
		}
	}
}

// Interval returns the polling interval
func (c *Collector) Interval() time.Duration {
	return c.config.UpdateInterval
}

// Poll executes one polling cycle
func (c *Collector) Poll(ctx context.Context) error {
	c.updateMetrics()
	return nil
}
