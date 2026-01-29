package userbalance

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

// Collector implements user balance monitoring
type Collector struct {
	*base.BaseCollector
	config   *Config
	logger   *log.Entry
	pgClient *pgxpool.Pool

	// Prometheus metrics
	balanceGauge *prometheus.Desc

	// Internal state
	mu       sync.RWMutex
	balances map[string]float64 // key: provider:accountID
}

// initMetrics initializes Prometheus metric descriptors
func (c *Collector) initMetrics(namespace string) {
	c.balanceGauge = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "userbalance", "balance"),
		"Current balance for each sealos user",
		[]string{"region", "uuid", "uid", "owner", "type", "level"},
		nil,
	)

	// Register descriptor
	c.MustRegisterDesc(c.balanceGauge)
}

// HasSynced returns true (polling collector is always synced)
func (c *Collector) HasSynced() bool {
	return true
}

// Interval returns the polling interval
func (c *Collector) Interval() time.Duration {
	return c.config.CheckInterval
}

// pollLoop periodically queries user balances
func (c *Collector) pollLoop(ctx context.Context) {
	// Initial poll
	_ = c.Poll(ctx)
	c.SetReady()

	ticker := time.NewTicker(c.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := c.Poll(ctx); err != nil {
				c.logger.WithError(err).Error("Failed to poll cloud balances")
			}
		case <-ctx.Done():
			c.logger.Info("Context cancelled, stopping cloud balance poll loop")
			return
		}
	}
}

// Poll queries all configured user accounts
func (c *Collector) Poll(ctx context.Context) error {
	if len(c.config.UserConfig) == 0 {
		c.logger.Debug("No sealos user configured for monitoring")
		return nil
	}

	c.logger.WithField("count", len(c.config.UserConfig)).Info("Starting cloud balance checks")

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, user := range c.config.UserConfig {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		balance, err := c.QueryBalance(user)
		if err != nil {
			c.logger.WithFields(log.Fields{
				"provider": c.pgClient,
				"user_id":  user.Uid,
			}).WithError(err).Error("Failed to query sealos user balance")

			continue
		}

		key := string(account.Provider) + ":" + account.AccountID
		c.balances[key] = balance

		c.logger.WithFields(log.Fields{
			"provider":   account.Provider,
			"account_id": account.AccountID,
			"balance":    balance,
		}).Debug("Cloud balance updated")
	}

	return nil
}
