package crds

import (
	"sync"

	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

// Collector collects crd (Custom Resource Definition) metrics
type Collector struct {
	*base.BaseCollector
	crdCollectors []*CrdCollector
	logger        *log.Entry
	mu            sync.RWMutex
}

func (c *Collector) collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, crdCollector := range c.crdCollectors {
		crdCollector.Collect(ch)
	}
}
