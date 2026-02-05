package crds

import "time"

// CollectorConfig is the top-level configuration for the configurable dynamic collector
type CollectorConfig struct {
	// CRDs defines the CRDs to monitor
	CRDs []CRDConfig `yaml:"crds" env:"CRDS"`
}

// CRDConfig defines configuration for monitoring a specific CRD
type CRDConfig struct {
	// Name is a unique identifier for this CRD monitoring config
	Name string `yaml:"name"`

	// GVR defines the GroupVersionResource to watch
	GVR GVRConfig `yaml:"gvr"`

	// Namespaces to watch (empty = all namespaces)
	Namespaces []string `yaml:"namespaces"`

	// ResyncPeriod is the resync interval for the informer
	ResyncPeriod time.Duration `yaml:"resyncPeriod"`

	// CommonLabels are labels extracted for all metrics from this CRD
	CommonLabels map[string]string `yaml:"commonLabels"`

	// Metrics defines what metrics to expose
	Metrics []MetricConfig `yaml:"metrics"`
}

// GVRConfig defines a GroupVersionResource
type GVRConfig struct {
	Group    string `yaml:"group"`
	Version  string `yaml:"version"`
	Resource string `yaml:"resource"`
}

// MetricConfig defines a metric to expose
type MetricConfig struct {
	// Type is the metric type: info, count, gauge, map_state, map_gauge, conditions
	// - info: Metadata labels (always value=1)
	// - count: Aggregate count of resources by field value (value=count)
	// - gauge: Numeric value from each resource
	// - map_state: Current state of each map entry (value=1)
	// - map_gauge: Numeric value from each map entry
	// - conditions: Kubernetes-style conditions
	Type string `yaml:"type"`

	// Name is the metric name (will be prefixed with namespace and CRD name)
	Name string `yaml:"name"`

	// Help is the metric help text
	Help string `yaml:"help"`

	// Path is the JSONPath to the field (e.g., "status.phase")
	Path string `yaml:"path"`

	// Labels are additional labels to extract (for info metrics)
	Labels map[string]string `yaml:"labels"`

	// ValueLabel is the label name for the aggregated value (for count metrics, default: "value")
	ValueLabel string `yaml:"valueLabel"`

	// ValuePath is the path to the value within each map entry (for map metrics)
	ValuePath string `yaml:"valuePath"`

	// KeyLabel is the label name for the map key (for map metrics)
	KeyLabel string `yaml:"keyLabel"`

	// ConditionConfig defines how to parse conditions
	Condition *ConditionConfig `yaml:"condition"`
}

// ConditionConfig defines how to parse Kubernetes-style conditions
type ConditionConfig struct {
	// TypeField is the field name for condition type (default: "type")
	TypeField string `yaml:"typeField"`

	// StatusField is the field name for condition status (default: "status")
	StatusField string `yaml:"statusField"`

	// ReasonField is the field name for condition reason (default: "reason")
	ReasonField string `yaml:"reasonField"`
}

// NewDefaultConfig creates a new CollectorConfig with default values
func NewDefaultConfig() *CollectorConfig {
	return &CollectorConfig{
		CRDs: []CRDConfig{},
	}
}
