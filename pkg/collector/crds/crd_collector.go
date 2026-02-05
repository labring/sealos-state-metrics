// Package crds pkg/collector/crds/crd_collector.go
package crds

import (
	"errors"
	"strings"

	"github.com/labring/sealos-state-metrics/pkg/collector/crds/helpers"
	informer "github.com/labring/sealos-state-metrics/pkg/collector/crds/informer"
	"github.com/labring/sealos-state-metrics/pkg/collector/crds/store"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// CrdCollector implements Prometheus metrics collection.
// Responsibilities: Reads resources from ResourceStore and generates Prometheus metrics.
type CrdCollector struct {
	// Configuration
	crdConfig    *CRDConfig
	metricPrefix string

	// Metric descriptors (built during initialization)
	descriptors map[string]*prometheus.Desc

	// Storage layer reference (read-only dependency)
	store *store.ResourceStore

	// Informer reference (for health checks)
	informer *informer.Informer

	// Logger instance
	logger *log.Entry
}

// NewCrdCollector creates a new CRD collector.
// Parameters:
//   - crdConfig: CRD metrics configuration
//   - resourceStore: Resource storage layer (dependency injection)
//   - informer: Informer instance (for health checks)
//   - metricPrefix: Metric name prefix
//   - logger: Logger instance
func NewCrdCollector(
	crdConfig *CRDConfig,
	resourceStore *store.ResourceStore,
	informer *informer.Informer,
	metricPrefix string,
	logger *log.Entry,
) (*CrdCollector, error) {
	// Validate parameters
	if crdConfig == nil {
		return nil, errors.New("crdConfig cannot be nil")
	}

	if resourceStore == nil {
		return nil, errors.New("resource store cannot be nil")
	}

	if informer == nil {
		return nil, errors.New("informer cannot be nil")
	}

	if metricPrefix == "" {
		metricPrefix = "sealos" // Default prefix
	}

	// Create logger instance
	if logger == nil {
		logger = log.WithField("component", "crd_collector")
	}

	collectorLogger := logger.WithFields(log.Fields{
		"prefix":        metricPrefix,
		"resource_type": resourceStore.GetResourceType(),
	})

	collector := &CrdCollector{
		crdConfig:    crdConfig,
		metricPrefix: metricPrefix,
		store:        resourceStore,
		informer:     informer,
		logger:       collectorLogger,
		descriptors:  make(map[string]*prometheus.Desc),
	}

	collector.buildDescriptors()
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

	// Read all resources from ResourceStore
	resources := c.store.List()

	c.logger.WithField("resource_count", len(resources)).Debug("Starting metric collection")

	// First pass: collect metrics for each resource
	for _, obj := range resources {
		// Extract common labels
		commonLabels := c.extractCommonLabels(obj)

		// Collect each configured metric
		for _, metricCfg := range c.crdConfig.Metrics {
			desc, ok := c.descriptors[metricCfg.Name]
			if !ok {
				continue
			}

			switch metricCfg.Type {
			case "info":
				c.collectInfoMetric(ch, desc, obj, &metricCfg, commonLabels)
			case "gauge":
				c.collectGaugeMetric(ch, desc, obj, &metricCfg, commonLabels)
			case "string_state":
				c.collectStringStateMetric(ch, desc, obj, &metricCfg, commonLabels)
			case "map_state":
				c.collectMapStateMetric(ch, desc, obj, &metricCfg, commonLabels)
			case "map_gauge":
				c.collectMapGaugeMetric(ch, desc, obj, &metricCfg, commonLabels)
			case "conditions":
				c.collectConditionsMetric(ch, desc, obj, &metricCfg, commonLabels)
			}
		}
	}

	// Second pass: collect aggregate metrics (count type)
	for _, metricCfg := range c.crdConfig.Metrics {
		if metricCfg.Type != "count" {
			continue
		}

		desc, ok := c.descriptors[metricCfg.Name]
		if !ok {
			continue
		}

		c.collectCountMetric(ch, desc, &metricCfg, resources)
	}

	c.logger.Debug("Metric collection completed")
}

// buildDescriptors builds descriptors for all metrics.
func (c *CrdCollector) buildDescriptors() {
	// Get sorted common label names (sorting ensures consistent order)
	commonLabelNames := helpers.GetSortedKeys(c.crdConfig.CommonLabels)

	for _, metricCfg := range c.crdConfig.Metrics {
		var (
			desc       *prometheus.Desc
			labelNames []string
		)

		switch metricCfg.Type {
		case "info":
			labelNames = make(
				[]string,
				len(commonLabelNames),
				len(commonLabelNames)+len(metricCfg.Labels),
			)
			copy(labelNames, commonLabelNames)
			labelNames = append(labelNames, helpers.GetSortedKeys(metricCfg.Labels)...)
		case "gauge":
			labelNames = make([]string, len(commonLabelNames))
			copy(labelNames, commonLabelNames)
		case "count":
			labelNames = make([]string, 1)
			labelNames[0] = "value"
		case "string_state":
			labelNames = make([]string, len(commonLabelNames), len(commonLabelNames)+1)
			copy(labelNames, commonLabelNames)
			labelNames = append(labelNames, "state")
		case "map_state":
			labelNames = make([]string, len(commonLabelNames), len(commonLabelNames)+2)
			copy(labelNames, commonLabelNames)
			labelNames = append(labelNames, "key", "state")
		case "map_gauge":
			labelNames = make([]string, len(commonLabelNames), len(commonLabelNames)+1)
			copy(labelNames, commonLabelNames)
			labelNames = append(labelNames, "key")
		case "conditions":
			labelNames = make([]string, len(commonLabelNames), len(commonLabelNames)+3)
			copy(labelNames, commonLabelNames)
			labelNames = append(labelNames, "type", "status", "reason")
		default:
			c.logger.WithField("type", metricCfg.Type).Warn("Unknown metric type, skipping")
			continue
		}

		// Build full metric name
		fullMetricName := prometheus.BuildFQName(c.metricPrefix, "", metricCfg.Name)

		// Create descriptor
		desc = prometheus.NewDesc(
			fullMetricName,
			metricCfg.Help,
			labelNames,
			nil, // Do not use ConstLabels
		)

		c.descriptors[metricCfg.Name] = desc

		c.logger.WithFields(log.Fields{
			"name":        fullMetricName,
			"type":        metricCfg.Type,
			"label_count": len(labelNames),
		}).Debug("Metric descriptor created")
	}
}

// collectInfoMetric collects info type metrics.
// Info metrics always have a value of 1, used to expose label information.
func (c *CrdCollector) collectInfoMetric(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	obj *unstructured.Unstructured,
	cfg *MetricConfig,
	commonLabels []string,
) {
	labels := make([]string, len(commonLabels), len(commonLabels)+len(cfg.Labels))
	copy(labels, commonLabels)

	// Add additional labels
	for _, path := range helpers.GetSortedValues(cfg.Labels) {
		value := helpers.ExtractFieldString(obj, path)
		labels = append(labels, value)
	}

	ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, 1, labels...)
}

// collectGaugeMetric collects gauge type metrics.
// Extracts numeric value from specified path.
func (c *CrdCollector) collectGaugeMetric(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	obj *unstructured.Unstructured,
	cfg *MetricConfig,
	commonLabels []string,
) {
	value := helpers.ExtractFieldFloat(obj, cfg.Path)

	ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, commonLabels...)
}

// collectCountMetric collects count type metrics (aggregate metrics).
// Counts the number of resources for each distinct field value.
func (c *CrdCollector) collectCountMetric(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	cfg *MetricConfig,
	resources []*unstructured.Unstructured,
) {
	// Count resources by field value
	valueCounts := make(map[string]float64)

	for _, obj := range resources {
		value := helpers.ExtractFieldString(obj, cfg.Path)
		if value != "" {
			valueCounts[value]++
		}
	}

	// Send metrics for each discovered value
	for value, count := range valueCounts {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, count, value)
	}
}

func (c *CrdCollector) collectMapStateMetric(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	obj *unstructured.Unstructured,
	cfg *MetricConfig,
	commonLabels []string,
) {
	mapData := helpers.ExtractFieldMap(obj, cfg.Path)

	for key, entryData := range mapData {
		entryMap, ok := entryData.(map[string]any)
		if !ok {
			continue
		}

		currentState, _ := entryMap[cfg.ValuePath].(string)

		// Only send metric when current state exists
		if currentState == "" {
			continue
		}

		labels := make([]string, len(commonLabels), len(commonLabels)+2)
		copy(labels, commonLabels)
		labels = append(labels, key, currentState)

		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, 1.0, labels...)
	}
}

func (c *CrdCollector) collectStringStateMetric(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	obj *unstructured.Unstructured,
	cfg *MetricConfig,
	commonLabels []string,
) {
	stringData := helpers.ExtractFieldString(obj, cfg.Path)
	metricValue := 1.0
	labels := make([]string, len(commonLabels)+1)
	copy(labels, commonLabels)

	labels[len(commonLabels)] = stringData
	ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, metricValue, labels...)
}

// collectMapGaugeMetric collects map_gauge type metrics.
// Exposes numeric values for each entry in the map.
func (c *CrdCollector) collectMapGaugeMetric(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	obj *unstructured.Unstructured,
	cfg *MetricConfig,
	commonLabels []string,
) {
	mapData := helpers.ExtractFieldMap(obj, cfg.Path)

	for key, entryData := range mapData {
		entryMap, ok := entryData.(map[string]any)
		if !ok {
			continue
		}

		value := 0.0
		if rawValue, ok := entryMap[cfg.ValuePath]; ok {
			value = helpers.ToFloat64(rawValue)
		}

		labels := make([]string, len(commonLabels), len(commonLabels)+1)
		copy(labels, commonLabels)
		labels = append(labels, key)

		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labels...)
	}
}

// collectConditionsMetric collects conditions type metrics.
// Exposes Kubernetes-style conditions array.
func (c *CrdCollector) collectConditionsMetric(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	obj *unstructured.Unstructured,
	cfg *MetricConfig,
	commonLabels []string,
) {
	// Default field names
	typeField := "type"
	statusField := "status"
	reasonField := "reason"

	// Use configured field names if provided
	if cfg.Condition != nil {
		if cfg.Condition.TypeField != "" {
			typeField = cfg.Condition.TypeField
		}

		if cfg.Condition.StatusField != "" {
			statusField = cfg.Condition.StatusField
		}

		if cfg.Condition.ReasonField != "" {
			reasonField = cfg.Condition.ReasonField
		}
	}

	conditions := helpers.ExtractFieldSlice(obj, cfg.Path)

	for _, condData := range conditions {
		condMap, ok := condData.(map[string]any)
		if !ok {
			continue
		}

		condType, _ := condMap[typeField].(string)
		condStatus, _ := condMap[statusField].(string)
		condReason, _ := condMap[reasonField].(string)

		if condType == "" {
			continue
		}

		labels := make([]string, len(commonLabels), len(commonLabels)+3)
		copy(labels, commonLabels)
		labels = append(labels, condType, condStatus, condReason)

		// Value is 1 when status is "True", otherwise 0
		value := 0.0
		if strings.EqualFold(condStatus, "true") {
			value = 1.0
		}

		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labels...)
	}
}

func (c *CrdCollector) extractCommonLabels(obj *unstructured.Unstructured) []string {
	labels := make([]string, 0, len(c.crdConfig.CommonLabels))
	for _, path := range helpers.GetSortedValues(c.crdConfig.CommonLabels) {
		value := helpers.ExtractFieldString(obj, path)
		labels = append(labels, value)
	}

	return labels
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
