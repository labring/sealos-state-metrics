package domain

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"github.com/zijiren233/sealos-state-metric/pkg/collector/base"
)

// Collector collects domain metrics
type Collector struct {
	*base.BaseCollector

	config  *Config
	checker *DomainChecker
	stopCh  chan struct{}
	logger  *log.Entry

	mu  sync.RWMutex
	ips map[string]*IPHealth // key: domain/ip

	// Metrics
	domainStatus       *prometheus.Desc
	domainCertExpiry   *prometheus.Desc
	domainResponseTime *prometheus.Desc
}

// initMetrics initializes Prometheus metric descriptors
func (c *Collector) initMetrics(namespace string) {
	c.domainStatus = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "domain", "status"),
		"Domain IP status (1=ok, 0=error)",
		[]string{"domain", "ip", "check_type", "error_type"},
		nil,
	)
	c.domainCertExpiry = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "domain", "cert_expiry_seconds"),
		"Domain certificate expiry in seconds",
		[]string{"domain", "ip", "error_type"},
		nil,
	)
	c.domainResponseTime = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "domain", "response_time_seconds"),
		"Domain IP response time in seconds",
		[]string{"domain", "ip"},
		nil,
	)

	// Register descriptors
	c.MustRegisterDesc(c.domainStatus)
	c.MustRegisterDesc(c.domainCertExpiry)
	c.MustRegisterDesc(c.domainResponseTime)
}

// Start starts the collector
func (c *Collector) Start(ctx context.Context) error {
	if err := c.BaseCollector.Start(ctx); err != nil {
		return err
	}

	// Start polling goroutine
	go c.pollLoop()

	c.logger.Info("Domain collector started successfully")

	return nil
}

// Stop stops the collector
func (c *Collector) Stop() error {
	close(c.stopCh)
	return c.BaseCollector.Stop()
}

// HasSynced returns true (polling collector is always synced)
func (c *Collector) HasSynced() bool {
	return true
}

// Interval returns the polling interval
func (c *Collector) Interval() time.Duration {
	return c.config.CheckInterval
}

// Poll performs one check cycle
func (c *Collector) Poll(ctx context.Context) error {
	if len(c.config.Domains) == 0 {
		c.logger.Debug("No domains configured for monitoring")
		return nil
	}

	c.logger.WithField("count", len(c.config.Domains)).Info("Starting domain health checks")

	// Create a new map to store all IP health results
	newIPs := make(map[string]*IPHealth)

	var mu sync.Mutex

	// Check domains concurrently
	var wg sync.WaitGroup
	for _, domain := range c.config.Domains {
		wg.Add(1)

		go func(d string) {
			defer wg.Done()

			ipHealths := c.checker.CheckIPs(ctx, d, c.logger)

			// Add IP health results to new map
			mu.Lock()

			for _, ipHealth := range ipHealths {
				key := ipKey(ipHealth.Domain, ipHealth.IP)
				newIPs[key] = ipHealth
			}

			mu.Unlock()
		}(domain)
	}

	wg.Wait()

	// Atomically replace the old map with the new one
	c.mu.Lock()
	c.ips = newIPs
	c.mu.Unlock()

	c.logger.WithField("count", len(c.config.Domains)).Info("Domain health checks completed")

	return nil
}

// pollLoop runs the polling loop
func (c *Collector) pollLoop() {
	ticker := time.NewTicker(c.config.CheckInterval)
	defer ticker.Stop()

	// Do initial check
	_ = c.Poll(c.Context())

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

	for _, ipHealth := range c.ips {
		// HTTP status
		if c.config.IncludeHTTPCheck {
			ch <- prometheus.MustNewConstMetric(
				c.domainStatus,
				prometheus.GaugeValue,
				boolToFloat64(ipHealth.HTTPOk),
				ipHealth.Domain,
				ipHealth.IP,
				"http",
				string(ipHealth.HTTPErrorType),
			)

			if ipHealth.HTTPOk {
				ch <- prometheus.MustNewConstMetric(
					c.domainResponseTime,
					prometheus.GaugeValue,
					ipHealth.ResponseTime.Seconds(),
					ipHealth.Domain,
					ipHealth.IP,
				)
			}
		}

		// Certificate status
		if c.config.IncludeCertCheck {
			ch <- prometheus.MustNewConstMetric(
				c.domainStatus,
				prometheus.GaugeValue,
				boolToFloat64(ipHealth.CertOk),
				ipHealth.Domain,
				ipHealth.IP,
				"cert",
				string(ipHealth.CertErrorType),
			)

			if ipHealth.CertOk && ipHealth.CertExpiry > 0 {
				ch <- prometheus.MustNewConstMetric(
					c.domainCertExpiry,
					prometheus.GaugeValue,
					ipHealth.CertExpiry.Seconds(),
					ipHealth.Domain,
					ipHealth.IP,
					string(ipHealth.CertErrorType),
				)
			}
		}
	}
}

// ipKey generates a unique key for an IP
func ipKey(domain, ip string) string {
	return domain + "/" + ip
}

// boolToFloat64 converts a boolean to a float64
func boolToFloat64(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}
