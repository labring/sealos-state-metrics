// Package kubeblocks provides a collector for monitoring KubeBlocks Cluster resources.
package kubeblocks

import (
	"fmt"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	dynamiccollector "github.com/labring/sealos-state-metrics/pkg/collector/dynamic"
	"github.com/labring/sealos-state-metrics/pkg/registry"
)

const collectorName = "kubeblocks"

func init() {
	registry.MustRegister(collectorName, NewCollector)
}

// NewCollector creates a new KubeBlocks Cluster collector using configuration-driven approach
func NewCollector(factoryCtx *collector.FactoryContext) (collector.Collector, error) {
	// Get Kubernetes rest config (lazy initialization)
	restConfig, err := factoryCtx.GetRestConfig()
	if err != nil {
		return nil, fmt.Errorf("kubernetes rest config is required but not available: %w", err)
	}

	// 1. Load KubeBlocks-specific configuration
	cfg := NewDefaultConfig()
	if err := factoryCtx.ConfigLoader.LoadModuleConfig("collectors.kubeblocks", cfg); err != nil {
		factoryCtx.Logger.WithError(err).
			Debug("Failed to load kubeblocks collector config, using defaults")
	}

	// 2. Generate CollectorConfig from KubeBlocks config
	collectorConfig := buildCollectorConfig(cfg)

	// 3. Create configurable collector using dynamic framework
	crdCfg := &collectorConfig.CRDs[0]

	return dynamiccollector.NewConfigurableCollectorFromConfig(
		collectorName,
		crdCfg,
		factoryCtx.MetricsNamespace,
		restConfig,
		factoryCtx.Logger,
	)
}

// buildCollectorConfig converts KubeBlocks Config to dynamiccollector.CollectorConfig
func buildCollectorConfig(cfg *Config) *dynamiccollector.CollectorConfig {
	return &dynamiccollector.CollectorConfig{
		CRDs: []dynamiccollector.CRDConfig{
			{
				Name: "kubeblocks-cluster",
				GVR: dynamiccollector.GVRConfig{
					Group:    "apps.kubeblocks.io",
					Version:  "v1alpha1",
					Resource: "clusters",
				},
				Namespaces:   cfg.Namespaces,
				ResyncPeriod: cfg.ResyncPeriod,
				CommonLabels: map[string]string{
					"namespace": "metadata.namespace",
					"cluster":   "metadata.name",
				},
				Metrics: []dynamiccollector.MetricConfig{
					// Cluster info metric (includes phase)
					{
						Type: "info",
						Name: "info",
						Help: "KubeBlocks Cluster information",
						Labels: map[string]string{
							"cluster_def":     "spec.clusterDefinitionRef",
							"cluster_version": "spec.clusterVersionRef",
							"phase":           "status.phase",
						},
					},
					// Cluster phase count (aggregate)
					{
						Type:       "count",
						Name:       "phase_count",
						Help:       "Count of clusters by phase",
						Path:       "status.phase",
						ValueLabel: "phase",
					},
				},
			},
		},
	}
}
