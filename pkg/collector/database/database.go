package database

import (
	"context"
	"sync"
	"time"

	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DatabaseType represents the type of database
type DatabaseType string

const (
	DatabaseTypeMySQL      DatabaseType = "mysql"
	DatabaseTypePostgreSQL DatabaseType = "postgresql"
	DatabaseTypeMongoDB    DatabaseType = "mongodb"
	DatabaseTypeRedis      DatabaseType = "redis"
)

// DatabaseStatus holds the connectivity status of a database
type DatabaseStatus struct {
	Name         string
	Namespace    string
	DatabaseType DatabaseType
	Connected    bool
	LastCheck    time.Time
	Error        string
	ResponseTime float64 // in seconds
}

// Collector implements database connectivity monitoring
type Collector struct {
	*base.BaseCollector
	config *Config
	logger *log.Entry
	client kubernetes.Interface

	// Prometheus metrics
	connectivityGauge *prometheus.Desc
	responseTimeGauge *prometheus.Desc

	// Internal state
	mu             sync.RWMutex
	dbConnectivity map[string]*DatabaseStatus // key: namespace/name
}

// initMetrics initializes Prometheus metric descriptors
func (c *Collector) initMetrics(namespace string) {
	c.connectivityGauge = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "database", "connectivity"),
		"Database connectivity status (1 = connected, 0 = disconnected)",
		[]string{"namespace", "database", "type"},
		nil,
	)

	c.responseTimeGauge = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "database", "response_time_seconds"),
		"Database connection response time in seconds",
		[]string{"namespace", "database", "type"},
		nil,
	)

	c.MustRegisterDesc(c.connectivityGauge)
	c.MustRegisterDesc(c.responseTimeGauge)
}

// HasSynced returns true (polling collector is always synced)
func (c *Collector) HasSynced() bool {
	return true
}

// Interval returns the polling interval
func (c *Collector) Interval() time.Duration {
	return c.config.CheckInterval
}

// pollLoop periodically checks database connectivity
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
				c.logger.WithError(err).Error("Failed to poll database connectivity")
			}
		case <-ctx.Done():
			c.logger.Info("Context cancelled, stopping database poll loop")
			return
		}
	}
}

// Poll checks all databases in the cluster
func (c *Collector) Poll(ctx context.Context) error {
	c.logger.Debug("Starting database connectivity checks")

	// Get list of namespaces to check
	namespaces, err := c.getNamespaces(ctx)
	if err != nil {
		return err
	}

	c.logger.WithField("namespaces", len(namespaces)).Debug("Scanning namespaces for databases")

	newStatus := make(map[string]*DatabaseStatus)

	for _, ns := range namespaces {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check MySQL databases
		if err := c.scanDatabases(ctx, ns, DatabaseTypeMySQL, newStatus); err != nil {
			c.logger.WithError(err).
				WithField("namespace", ns).
				Error("Failed to scan MySQL databases")
		}

		// Check PostgreSQL databases
		if err := c.scanDatabases(ctx, ns, DatabaseTypePostgreSQL, newStatus); err != nil {
			c.logger.WithError(err).
				WithField("namespace", ns).
				Error("Failed to scan PostgreSQL databases")
		}

		// Check MongoDB databases
		if err := c.scanDatabases(ctx, ns, DatabaseTypeMongoDB, newStatus); err != nil {
			c.logger.WithError(err).
				WithField("namespace", ns).
				Error("Failed to scan MongoDB databases")
		}

		// Check Redis databases
		if err := c.scanDatabases(ctx, ns, DatabaseTypeRedis, newStatus); err != nil {
			c.logger.WithError(err).
				WithField("namespace", ns).
				Error("Failed to scan Redis databases")
		}
	}

	// Update internal state
	c.mu.Lock()
	c.dbConnectivity = newStatus
	c.mu.Unlock()

	c.logger.WithField("databases", len(newStatus)).Info("Database connectivity check completed")

	return nil
}

// getNamespaces returns the list of namespaces to scan
func (c *Collector) getNamespaces(ctx context.Context) ([]string, error) {
	// If specific namespaces are configured, use them
	if len(c.config.Namespaces) > 0 {
		return c.config.Namespaces, nil
	}

	// Otherwise, get all namespaces
	nsList, err := c.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	namespaces := make([]string, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		namespaces = append(namespaces, ns.Name)
	}

	return namespaces, nil
}

// scanDatabases scans for databases of a specific type in a namespace
func (c *Collector) scanDatabases(
	ctx context.Context,
	namespace string,
	dbType DatabaseType,
	statusMap map[string]*DatabaseStatus,
) error {
	var secretSelector string

	switch dbType {
	case DatabaseTypeMySQL:
		// #nosec G101
		secretSelector = "app.kubernetes.io/name=apecloud-mysql"
	case DatabaseTypePostgreSQL:
		// #nosec G101
		secretSelector = "app.kubernetes.io/name=postgresql"
	case DatabaseTypeMongoDB:
		// #nosec G101
		secretSelector = "apps.kubeblocks.io/component-name=mongodb"
	case DatabaseTypeRedis:
		// #nosec G101
		secretSelector = "apps.kubeblocks.io/component-name=redis"
	default:
		return nil
	}

	// Find secrets with the appropriate labels
	secrets, err := c.client.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: secretSelector,
	})
	if err != nil {
		return err
	}

	for _, secret := range secrets.Items {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if this is a connection credential secret
		if !c.isCredentialSecret(&secret, dbType) {
			continue
		}

		// Extract database name from secret name
		dbName := c.extractDatabaseName(&secret, dbType)
		key := namespace + "/" + dbName

		c.logger.WithFields(log.Fields{
			"namespace": namespace,
			"database":  dbName,
			"type":      dbType,
		}).Debug("Checking database connectivity")

		// Check connectivity
		status := c.checkDatabaseConnectivity(ctx, namespace, dbName, dbType, &secret)
		statusMap[key] = status
	}

	return nil
}

// isCredentialSecret checks if the secret is a credential secret for the database type
func (c *Collector) isCredentialSecret(secret *corev1.Secret, dbType DatabaseType) bool {
	switch dbType {
	case DatabaseTypeMySQL, DatabaseTypePostgreSQL:
		suffix := "-conn-credential"
		return len(secret.Name) > len(suffix) &&
			secret.Name[len(secret.Name)-len(suffix):] == suffix
	case DatabaseTypeMongoDB:
		suffix := "-mongodb-account-root"
		return len(secret.Name) > len(suffix) &&
			secret.Name[len(secret.Name)-len(suffix):] == suffix
	case DatabaseTypeRedis:
		suffix := "-redis-account-default"
		return len(secret.Name) > len(suffix) &&
			secret.Name[len(secret.Name)-len(suffix):] == suffix
	}

	return false
}

// extractDatabaseName extracts the database name from the secret name
func (c *Collector) extractDatabaseName(secret *corev1.Secret, dbType DatabaseType) string {
	switch dbType {
	case DatabaseTypeMySQL, DatabaseTypePostgreSQL:
		// Remove "-conn-credential" suffix
		if len(secret.Name) > len("-conn-credential") {
			return secret.Name[:len(secret.Name)-len("-conn-credential")]
		}
	case DatabaseTypeMongoDB:
		// Remove "-mongodb-account-root" suffix
		if len(secret.Name) > len("-mongodb-account-root") {
			return secret.Name[:len(secret.Name)-len("-mongodb-account-root")]
		}
	case DatabaseTypeRedis:
		// Remove "-redis-account-default" suffix
		if len(secret.Name) > len("-redis-account-default") {
			return secret.Name[:len(secret.Name)-len("-redis-account-default")]
		}
	}

	return secret.Name
}

// checkDatabaseConnectivity checks if a database is accessible
func (c *Collector) checkDatabaseConnectivity(
	ctx context.Context,
	namespace, dbName string,
	dbType DatabaseType,
	secret *corev1.Secret,
) *DatabaseStatus {
	status := &DatabaseStatus{
		Name:         dbName,
		Namespace:    namespace,
		DatabaseType: dbType,
		LastCheck:    time.Now(),
		Connected:    false,
	}

	// Create context with timeout
	checkCtx, cancel := context.WithTimeout(ctx, c.config.CheckTimeout)
	defer cancel()

	startTime := time.Now()

	var err error
	switch dbType {
	case DatabaseTypeMySQL:
		err = c.checkMySQLConnectivity(checkCtx, secret)
	case DatabaseTypePostgreSQL:
		err = c.checkPostgreSQLConnectivity(checkCtx, namespace, secret)
	case DatabaseTypeMongoDB:
		err = c.checkMongoDBConnectivity(checkCtx, namespace, dbName, secret)
	case DatabaseTypeRedis:
		err = c.checkRedisConnectivity(checkCtx, namespace, dbName, secret)
	default:
		err = nil
	}

	status.ResponseTime = time.Since(startTime).Seconds()

	if err != nil {
		status.Connected = false
		status.Error = err.Error()
		c.logger.WithFields(log.Fields{
			"namespace": namespace,
			"database":  dbName,
			"type":      dbType,
		}).WithError(err).Warn("Database connectivity check failed")
	} else {
		status.Connected = true
		status.Error = ""
		c.logger.WithFields(log.Fields{
			"namespace":     namespace,
			"database":      dbName,
			"type":          dbType,
			"response_time": status.ResponseTime,
		}).Debug("Database connectivity check succeeded")
	}

	return status
}

// collect implements the collect method for Prometheus
func (c *Collector) collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, status := range c.dbConnectivity {
		// Connectivity metric
		connectivityValue := 0.0
		if status.Connected {
			connectivityValue = 1.0
		}

		ch <- prometheus.MustNewConstMetric(
			c.connectivityGauge,
			prometheus.GaugeValue,
			connectivityValue,
			status.Namespace,
			status.Name,
			string(status.DatabaseType),
		)

		// Response time metric
		ch <- prometheus.MustNewConstMetric(
			c.responseTimeGauge,
			prometheus.GaugeValue,
			status.ResponseTime,
			status.Namespace,
			status.Name,
			string(status.DatabaseType),
		)
	}
}
