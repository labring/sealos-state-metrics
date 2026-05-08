// Package crds pkg/collector/crds/crd_collector.go
package crds

import (
	"errors"

	informer "github.com/labring/sealos-state-metrics/pkg/collector/crds/informer"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

// CrdCollector implements Prometheus metrics collection.
// Responsibilities: reads precomputed metrics from metricStore and exposes them.
type CrdCollector struct {
	// Configuration
	crdConfig    *CRDConfig
	metricPrefix string

	// Metric descriptors (built during initialization)
	descriptors map[string]*prometheus.Desc

	// Metric storage layer. It stores generated metrics instead of full CR objects.
	store *metricStore

	// Informer reference (for health checks)
	informer *informer.Informer

	// Logger instance
	logger *log.Entry
}

// NewCrdCollector creates a new CRD collector.
// Parameters:
//   - crdConfig: CRD metrics configuration
//   - metricStore: derived metric storage layer (dependency injection)
//   - informer: Informer instance (for health checks)
//   - metricPrefix: Metric name prefix
//   - logger: Logger instance
func NewCrdCollector(
	crdConfig *CRDConfig,
	metricStore *metricStore,
	informer *informer.Informer,
	metricPrefix string,
	logger *log.Entry,
) (*CrdCollector, error) {
	// Validate parameters
	if crdConfig == nil {
		return nil, errors.New("crdConfig cannot be nil")
	}

	if metricStore == nil {
		return nil, errors.New("metric store cannot be nil")
	}

	if informer == nil {
		return nil, errors.New("informer cannot be nil")
	}

	if metricPrefix == "" {
		metricPrefix = defaultMetricPrefix
	}

	// Create logger instance
	if logger == nil {
		logger = log.WithField("component", "crd_collector")
	}

	collectorLogger := logger.WithFields(log.Fields{
		"prefix":        metricPrefix,
		"resource_type": crdConfig.Name,
	})

	collector := &CrdCollector{
		crdConfig:    crdConfig,
		metricPrefix: metricPrefix,
		store:        metricStore,
		informer:     informer,
		logger:       collectorLogger,
		descriptors:  metricStore.Descriptors(),
	}

	collectorLogger.WithField("metric_count", len(collector.descriptors)).
		Info("CRD collector initialized")

	return collector, nil
}

// Describe implements prometheus.Collector interface.
// Sends all possible metric descriptors.
func (c *CrdCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range c.descriptors {
		ch <- desc
	}
}

// Collect implements prometheus.Collector interface.
// Collects and sends all metrics.
func (c *CrdCollector) Collect(ch chan<- prometheus.Metric) {
	// Check if Informer cache is synced
	if !c.informer.HasSynced() {
		c.logger.Warn("Informer cache not synced, skipping collection")
		return
	}

	c.store.Collect(ch)
	c.logger.Debug("Metric collection completed")
}

// GetMetricCount returns the number of configured metrics.
func (c *CrdCollector) GetMetricCount() int {
	return len(c.descriptors)
}

// GetResourceCount returns the number of currently cached resources.
func (c *CrdCollector) GetResourceCount() int {
	return c.store.Len()
}

// LogStats outputs collector statistics (for debugging).
func (c *CrdCollector) LogStats() {
	storeMetrics := c.store.GetMetrics()

	c.logger.WithFields(log.Fields{
		"metric_count":    len(c.descriptors),
		"resource_count":  storeMetrics.TotalCount,
		"informer_synced": c.informer.HasSynced(),
	}).Info("Collector statistics")
}
