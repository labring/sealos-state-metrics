// Package crds pkg/collector/crds/crd_collector.go
package crds

import (
	"fmt"
	"strings"

	"github.com/labring/sealos-state-metrics/pkg/collector/crds/helpers"
	informer "github.com/labring/sealos-state-metrics/pkg/collector/crds/informer"
	"github.com/labring/sealos-state-metrics/pkg/collector/crds/store"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// CrdCollector Prometheus 指标收集器
// 职责：从 ResourceStore 读取资源并生成 Prometheus 指标
type CrdCollector struct {
	// 配置
	crdConfig    *CRDConfig
	metricPrefix string

	// 指标描述符（在初始化时构建）
	descriptors map[string]*prometheus.Desc

	// 存储层引用（只读依赖）
	store *store.ResourceStore

	// Informer 引用（用于健康检查）
	informer *informer.Informer

	// 日志
	logger *log.Entry
}

// NewCrdCollector 创建新的 CRD 收集器
// 参数：
//   - crdConfig: CRD 指标配置
//   - resourceStore: 资源存储层（依赖注入）
//   - informer: Informer 实例（用于健康检查）
//   - metricPrefix: 指标名称前缀
//   - logger: 日志记录器
func NewCrdCollector(
	crdConfig *CRDConfig,
	resourceStore *store.ResourceStore,
	informer *informer.Informer,
	metricPrefix string,
	logger *log.Entry,
) (*CrdCollector, error) {
	// 参数验证
	if crdConfig == nil {
		return nil, fmt.Errorf("crdConfig cannot be nil")
	}

	if resourceStore == nil {
		return nil, fmt.Errorf("resource store cannot be nil")
	}

	if informer == nil {
		return nil, fmt.Errorf("informer cannot be nil")
	}

	if metricPrefix == "" {
		metricPrefix = "sealos" // 默认前缀
	}

	// 创建日志记录器
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

	// 构建指标描述符
	if err := collector.buildDescriptors(); err != nil {
		return nil, fmt.Errorf("failed to build descriptors: %w", err)
	}

	collectorLogger.WithField("metric_count", len(collector.descriptors)).
		Info("CRD collector initialized")

	return collector, nil
}

// Describe 实现 prometheus.Collector 接口
// 发送所有可能的指标描述符
func (c *CrdCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range c.descriptors {
		ch <- desc
	}
}

// Collect 实现 prometheus.Collector 接口
// 收集并发送所有指标
func (c *CrdCollector) Collect(ch chan<- prometheus.Metric) {
	// 检查 Informer 是否已同步
	if !c.informer.HasSynced() {
		c.logger.Warn("Informer cache not synced, skipping collection")
		return
	}

	// 从 ResourceStore 读取所有资源
	resources := c.store.List()

	c.logger.WithField("resource_count", len(resources)).Debug("Starting metric collection")

	// 第一遍：收集每个资源的指标
	for _, obj := range resources {
		// 提取通用标签
		commonLabels := c.extractCommonLabels(obj)

		// 收集每个配置的指标
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

	// 第二遍：收集聚合指标（count 类型）
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

// buildDescriptors 构建所有指标的描述符
func (c *CrdCollector) buildDescriptors() error {
	// 获取通用标签名称列表（需要排序以保证顺序一致）
	commonLabelNames := helpers.GetSortedKeys(c.crdConfig.CommonLabels)

	for _, metricCfg := range c.crdConfig.Metrics {
		var labelNames []string
		var desc *prometheus.Desc

		// 根据指标类型构建标签列表
		switch metricCfg.Type {
		case "info":
			// info 类型：通用标签 + 额外标签
			labelNames = make([]string, len(commonLabelNames))
			copy(labelNames, commonLabelNames)
			labelNames = append(labelNames, helpers.GetSortedKeys(metricCfg.Labels)...)

		case "gauge":
			// gauge 类型：只有通用标签
			labelNames = make([]string, len(commonLabelNames))
			copy(labelNames, commonLabelNames)

		case "count":
			// count 类型：只有一个标签（字段值）
			labelNames = []string{"value"}
		case "string_state":
			// map_state 类型：通用标签 + key + state
			labelNames = make([]string, len(commonLabelNames))
			copy(labelNames, commonLabelNames)
			labelNames = append(labelNames, "key", "state")

		case "map_state":
			// map_state 类型：通用标签 + key + state
			labelNames = make([]string, len(commonLabelNames))
			copy(labelNames, commonLabelNames)
			labelNames = append(labelNames, "key", "state")

		case "map_gauge":
			// map_gauge 类型：通用标签 + key
			labelNames = make([]string, len(commonLabelNames))
			copy(labelNames, commonLabelNames)
			labelNames = append(labelNames, "key")

		case "conditions":
			// conditions 类型：通用标签 + type + status + reason
			labelNames = make([]string, len(commonLabelNames))
			copy(labelNames, commonLabelNames)
			labelNames = append(labelNames, "type", "status", "reason")

		default:
			c.logger.WithField("type", metricCfg.Type).Warn("Unknown metric type, skipping")
			continue
		}

		// 构建完整的指标名称
		fullMetricName := prometheus.BuildFQName(c.metricPrefix, "", metricCfg.Name)

		// 创建描述符
		desc = prometheus.NewDesc(
			fullMetricName,
			metricCfg.Help,
			labelNames,
			nil, // 不使用 ConstLabels
		)

		c.descriptors[metricCfg.Name] = desc

		c.logger.WithFields(log.Fields{
			"name":        fullMetricName,
			"type":        metricCfg.Type,
			"label_count": len(labelNames),
		}).Debug("Metric descriptor created")
	}

	return nil
}

// collectInfoMetric 收集 info 类型指标
// info 指标的值始终为 1，用于暴露标签信息
func (c *CrdCollector) collectInfoMetric(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	obj *unstructured.Unstructured,
	cfg *MetricConfig,
	commonLabels []string,
) {
	labels := make([]string, len(commonLabels), len(commonLabels)+len(cfg.Labels))
	copy(labels, commonLabels)

	// 添加额外标签
	for _, path := range helpers.GetSortedValues(cfg.Labels) {
		value := helpers.ExtractFieldString(obj, path)
		labels = append(labels, value)
	}

	ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, 1, labels...)
}

// collectGaugeMetric 收集 gauge 类型指标
// 从指定路径提取数值
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

// collectCountMetric 收集 count 类型指标（聚合指标）
// 统计每个不同字段值的资源数量
func (c *CrdCollector) collectCountMetric(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	cfg *MetricConfig,
	resources []*unstructured.Unstructured,
) {
	// 按字段值统计资源数量
	valueCounts := make(map[string]float64)

	for _, obj := range resources {
		value := helpers.ExtractFieldString(obj, cfg.Path)
		if value != "" {
			valueCounts[value]++
		}
	}

	// 为每个发现的值发送指标
	for value, count := range valueCounts {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, count, value)
	}
}

// func (c *CrdCollector) collectMapStateMetric(
// 	ch chan<- prometheus.Metric,
// 	desc *prometheus.Desc,
// 	obj *unstructured.Unstructured,
// 	cfg *MetricConfig,
// 	commonLabels []string,
// ) {
// 	mapData := helpers.ExtractFieldMap(obj, cfg.Path)
//
// 	for key, entryData := range mapData {
// 		entryMap, ok := entryData.(map[string]any)
// 		if !ok {
// 			continue
// 		}
//
// 		currentState, _ := entryMap[cfg.ValuePath].(string)
//
// 		// 只在有当前状态时发送指标
// 		if currentState == "" {
// 			continue
// 		}
//
// 		labels := make([]string, len(commonLabels), len(commonLabels)+2)
// 		copy(labels, commonLabels)
// 		labels = append(labels, key, currentState)
//
// 		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, 1.0, labels...)
// 	}
// }

// collectMapStateMetric 收集 map_state 类型指标
// 用于暴露 map 中每个条目的状态，值为 1
func (c *CrdCollector) collectMapStateMetric(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	obj *unstructured.Unstructured,
	cfg *MetricConfig,
	commonLabels []string,
) {
	// 记录开始处理
	c.logger.WithFields(log.Fields{
		"metric_name":   cfg.Name,
		"field_path":    cfg.Path,
		"value_path":    cfg.ValuePath,
		"resource_name": obj.GetName(),
		"namespace":     obj.GetNamespace(),
	}).Info("Starting map_state metric collection")

	mapData := helpers.ExtractFieldMap(obj, cfg.Path)
	m

	// 记录提取的 map 数据
	c.logger.WithFields(log.Fields{
		"metric_name":   cfg.Name,
		"field_path":    cfg.Path,
		"map_size":      len(mapData),
		"map_keys":      fmt.Sprintf("%v", getMapKeys(mapData)),
		"resource_name": obj.GetName(),
		"namespace":     obj.GetNamespace(),
	}).Info("Extracted map data from resource")

	// 如果 map 为空，记录警告
	if len(mapData) == 0 {
		c.logger.WithFields(log.Fields{
			"metric_name":   cfg.Name,
			"field_path":    cfg.Path,
			"resource_name": obj.GetName(),
			"namespace":     obj.GetNamespace(),
		}).Warn("Map data is empty, no metrics will be generated")
		return
	}

	metricsGenerated := 0
	for key, entryData := range mapData {
		c.logger.WithFields(log.Fields{
			"metric_name":   cfg.Name,
			"map_key":       key,
			"entry_type":    fmt.Sprintf("%T", entryData),
			"resource_name": obj.GetName(),
			"namespace":     obj.GetNamespace(),
		}).Info("Processing map entry")

		entryMap, ok := entryData.(map[string]any)
		if !ok {
			c.logger.WithFields(log.Fields{
				"metric_name":   cfg.Name,
				"map_key":       key,
				"expected_type": "map[string]any",
				"actual_type":   fmt.Sprintf("%T", entryData),
				"entry_value":   fmt.Sprintf("%v", entryData),
				"resource_name": obj.GetName(),
				"namespace":     obj.GetNamespace(),
			}).Warn("Entry is not a map, skipping")
			continue
		}

		c.logger.WithFields(log.Fields{
			"metric_name":   cfg.Name,
			"map_key":       key,
			"entry_fields":  fmt.Sprintf("%v", getMapKeys(entryMap)),
			"value_path":    cfg.ValuePath,
			"resource_name": obj.GetName(),
			"namespace":     obj.GetNamespace(),
		}).Info("Entry is a valid map, extracting state")

		currentState, _ := entryMap[cfg.ValuePath].(string)

		// 记录状态提取结果
		c.logger.WithFields(log.Fields{
			"metric_name":   cfg.Name,
			"map_key":       key,
			"value_path":    cfg.ValuePath,
			"current_state": currentState,
			"state_empty":   currentState == "",
			"resource_name": obj.GetName(),
			"namespace":     obj.GetNamespace(),
		}).Info("Extracted state from entry")

		// 只在有当前状态时发送指标
		if currentState == "" {
			c.logger.WithFields(log.Fields{
				"metric_name":   cfg.Name,
				"map_key":       key,
				"value_path":    cfg.ValuePath,
				"resource_name": obj.GetName(),
				"namespace":     obj.GetNamespace(),
			}).Warn("Current state is empty, skipping metric generation")
			continue
		}

		labels := make([]string, len(commonLabels), len(commonLabels)+2)
		copy(labels, commonLabels)
		labels = append(labels, key, currentState)

		c.logger.WithFields(log.Fields{
			"metric_name":   cfg.Name,
			"map_key":       key,
			"current_state": currentState,
			"labels":        fmt.Sprintf("%v", labels),
			"resource_name": obj.GetName(),
			"namespace":     obj.GetNamespace(),
		}).Info("Generating metric")

		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, 1.0, labels...)
		metricsGenerated++
	}

	// 记录总结信息
	c.logger.WithFields(log.Fields{
		"metric_name":       cfg.Name,
		"field_path":        cfg.Path,
		"total_entries":     len(mapData),
		"metrics_generated": metricsGenerated,
		"resource_name":     obj.GetName(),
		"namespace":         obj.GetNamespace(),
	}).Info("Completed map_state metric collection")
}

// 辅助函数：获取 map 的所有 key
func getMapKeys(m map[string]any) []string {
	if m == nil {
		return []string{}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// collectStringStateMetric 收集 string 类型的状态指标
// 用于从简单字符串字段提取状态值
func (c *CrdCollector) collectStringStateMetric(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	obj *unstructured.Unstructured,
	cfg *MetricConfig,
	commonLabels []string,
) {
	// 记录开始处理
	c.logger.WithFields(log.Fields{
		"metric_name":   cfg.Name,
		"field_path":    cfg.Path,
		"resource_name": obj.GetName(),
		"namespace":     obj.GetNamespace(),
	}).Info("Starting string_state metric collection")

	// 提取字符串值
	value, found, err := unstructured.NestedString(obj.Object, strings.Split(cfg.Path, ".")...)
	if err != nil {
		c.logger.WithFields(log.Fields{
			"metric_name":   cfg.Name,
			"field_path":    cfg.Path,
			"error":         err.Error(),
			"resource_name": obj.GetName(),
			"namespace":     obj.GetNamespace(),
		}).Warn("Failed to extract string value")
		return
	}

	if !found {
		c.logger.WithFields(log.Fields{
			"metric_name":   cfg.Name,
			"field_path":    cfg.Path,
			"resource_name": obj.GetName(),
			"namespace":     obj.GetNamespace(),
		}).Warn("String field not found")
		return
	}

	// 记录提取到的值
	c.logger.WithFields(log.Fields{
		"metric_name":   cfg.Name,
		"field_path":    cfg.Path,
		"value":         value,
		"value_empty":   value == "",
		"resource_name": obj.GetName(),
		"namespace":     obj.GetNamespace(),
	}).Info("Extracted string value from resource")

	// 如果值为空，跳过
	if value == "" {
		c.logger.WithFields(log.Fields{
			"metric_name":   cfg.Name,
			"field_path":    cfg.Path,
			"resource_name": obj.GetName(),
			"namespace":     obj.GetNamespace(),
		}).Warn("String value is empty, skipping metric generation")
		return
	}

	// 查找值映射
	metricValue := 0.0
	if cfg.ValueMapping != nil {
		if mappedValue, ok := cfg.ValueMapping[value]; ok {
			metricValue = float64(mappedValue)
			c.logger.WithFields(log.Fields{
				"metric_name":   cfg.Name,
				"string_value":  value,
				"mapped_value":  metricValue,
				"resource_name": obj.GetName(),
				"namespace":     obj.GetNamespace(),
			}).Info("Value mapped successfully")
		} else {
			c.logger.WithFields(log.Fields{
				"metric_name":        cfg.Name,
				"string_value":       value,
				"available_mappings": getMapKeys(cfg.ValueMapping),
				"resource_name":      obj.GetName(),
				"namespace":          obj.GetNamespace(),
			}).Warn("Value not found in mapping, skipping")
			return
		}
	} else {
		// 如果没有映射，默认值为 1
		metricValue = 1.0
		c.logger.WithFields(log.Fields{
			"metric_name":   cfg.Name,
			"string_value":  value,
			"resource_name": obj.GetName(),
			"namespace":     obj.GetNamespace(),
		}).Info("No value mapping defined, using default value 1")
	}

	// 构建标签（包含状态值）
	labels := make([]string, len(commonLabels), len(commonLabels)+1)
	copy(labels, commonLabels)
	labels = append(labels, value)

	c.logger.WithFields(log.Fields{
		"metric_name":   cfg.Name,
		"string_value":  value,
		"metric_value":  metricValue,
		"labels":        fmt.Sprintf("%v", labels),
		"resource_name": obj.GetName(),
		"namespace":     obj.GetNamespace(),
	}).Info("Generating metric")

	// 发送指标
	ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, metricValue, labels...)

	c.logger.WithFields(log.Fields{
		"metric_name":   cfg.Name,
		"field_path":    cfg.Path,
		"string_value":  value,
		"metric_value":  metricValue,
		"resource_name": obj.GetName(),
		"namespace":     obj.GetNamespace(),
	}).Info("Completed string_state metric collection")
}

// collectMapGaugeMetric 收集 map_gauge 类型指标
// 用于暴露 map 中每个条目的数值
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

// collectConditionsMetric 收集 conditions 类型指标
// 用于暴露 K8s 风格的 conditions 数组
func (c *CrdCollector) collectConditionsMetric(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	obj *unstructured.Unstructured,
	cfg *MetricConfig,
	commonLabels []string,
) {
	// 默认字段名
	typeField := "type"
	statusField := "status"
	reasonField := "reason"

	// 使用配置的字段名（如果有）
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

		// 状态为 "True" 时值为 1，否则为 0
		value := 0.0
		if strings.EqualFold(condStatus, "true") {
			value = 1.0
		}

		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labels...)
	}
}

// extractCommonLabels 从对象中提取通用标签值
func (c *CrdCollector) extractCommonLabels(obj *unstructured.Unstructured) []string {
	labels := make([]string, 0, len(c.crdConfig.CommonLabels))

	// 按排序后的键顺序提取标签值
	for _, path := range helpers.GetSortedValues(c.crdConfig.CommonLabels) {
		value := helpers.ExtractFieldString(obj, path)
		labels = append(labels, value)
	}

	return labels
}

// GetMetricCount 返回配置的指标数量
func (c *CrdCollector) GetMetricCount() int {
	return len(c.descriptors)
}

// GetResourceCount 返回当前缓存的资源数量
func (c *CrdCollector) GetResourceCount() int {
	return c.store.Len()
}

// LogStats 输出收集器统计信息（用于调试）
func (c *CrdCollector) LogStats() {
	storeMetrics := c.store.GetMetrics()

	c.logger.WithFields(log.Fields{
		"metric_count":    len(c.descriptors),
		"resource_count":  storeMetrics.TotalCount,
		"informer_synced": c.informer.HasSynced(),
	}).Info("Collector statistics")
}
