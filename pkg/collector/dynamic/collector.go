// Package dynamic provides a generic dynamic client-based collector for monitoring CRDs.
package dynamic

import (
	"context"
	"errors"
	"fmt"

	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// Config defines the configuration for a dynamic collector
type Config struct {
	// GVR is the GroupVersionResource to watch
	GVR schema.GroupVersionResource

	// Namespaces to watch (empty slice means all namespaces)
	Namespaces []string

	// EventHandler is the callback interface for resource events
	EventHandler EventHandler

	// MetricsCollector is the function to collect metrics
	MetricsCollector func(ch chan<- prometheus.Metric)

	// MetricDescriptors are the Prometheus metric descriptors to register
	MetricDescriptors []*prometheus.Desc
}

// Collector is a generic dynamic client collector that watches CRDs
type Collector struct {
	*base.BaseCollector

	config        *Config
	dynamicClient dynamic.Interface
	controllers   []*Controller
	logger        *log.Entry
}

// NewCollector creates a new dynamic collector
func NewCollector(
	name string,
	dynamicClient dynamic.Interface,
	config *Config,
	logger *log.Entry,
	opts ...base.BaseCollectorOption,
) (*Collector, error) {
	if config == nil {
		return nil, errors.New("config cannot be nil")
	}

	if config.EventHandler == nil {
		return nil, errors.New("event handler cannot be nil")
	}

	if dynamicClient == nil {
		return nil, errors.New("dynamic client cannot be nil")
	}

	// Create base collector with default options
	defaultOpts := make([]base.BaseCollectorOption, 0, 2+len(opts))
	defaultOpts = append(defaultOpts,
		base.WithLeaderElection(true),
		base.WithWaitReadyOnCollect(true),
	)
	defaultOpts = append(defaultOpts, opts...)

	c := &Collector{
		BaseCollector: base.NewBaseCollector(name, logger, defaultOpts...),
		config:        config,
		dynamicClient: dynamicClient,
		logger:        logger,
	}

	// Register metric descriptors
	for _, desc := range config.MetricDescriptors {
		c.MustRegisterDesc(desc)
	}

	// Set lifecycle hooks
	c.SetLifecycle(base.LifecycleFuncs{
		StartFunc: func(ctx context.Context) error {
			return c.start(ctx)
		},
		StopFunc: func() error {
			return c.stop()
		},
		CollectFunc: func(ch chan<- prometheus.Metric) {
			if config.MetricsCollector != nil {
				config.MetricsCollector(ch)
			}
		},
	})

	return c, nil
}

// start starts all controllers
func (c *Collector) start(ctx context.Context) error {
	namespaces := c.config.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{""} // Empty string means all namespaces
	}

	c.controllers = make([]*Controller, 0, len(namespaces))

	for _, ns := range namespaces {
		loggerWithNs := c.logger
		if ns != "" {
			loggerWithNs = c.logger.WithField("namespace", ns)
		}

		controllerConfig := &ControllerConfig{
			GVR:          c.config.GVR,
			Namespace:    ns,
			ResyncPeriod: 0, // Use default
			EventHandler: c.config.EventHandler,
		}

		controller, err := NewController(c.dynamicClient, controllerConfig, loggerWithNs)
		if err != nil {
			return fmt.Errorf("failed to create controller for namespace %s: %w", ns, err)
		}

		if err := controller.Start(ctx); err != nil {
			return fmt.Errorf("failed to start controller for namespace %s: %w", ns, err)
		}

		c.controllers = append(c.controllers, controller)
	}

	// Mark as ready after all controllers have synced
	c.SetReady()

	c.logger.Info("Dynamic collector started successfully")

	return nil
}

// stop stops all controllers
func (c *Collector) stop() error {
	for _, ctrl := range c.controllers {
		if err := ctrl.Stop(); err != nil {
			c.logger.WithError(err).Warn("Failed to stop controller")
		}
	}

	return nil
}
