package database

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
	corev1 "k8s.io/api/core/v1"
)

// PostgreSQLConnectionInfo holds PostgreSQL connection information
type PostgreSQLConnectionInfo struct {
	Username string
	password string
	Host     string
	Port     string
	Endpoint string
	DSN      string
}

// checkPostgreSQLConnectivity checks PostgreSQL database connectivity
func (c *Collector) checkPostgreSQLConnectivity(
	ctx context.Context,
	namespace string,
	secret *corev1.Secret,
) error {
	// 1. Parse connection information
	connInfo, err := c.parsePostgreSQLConnectionInfo(secret, namespace)
	if err != nil {
		return fmt.Errorf("failed to parse connection info: %w", err)
	}

	c.logger.Debugf("Connecting to PostgreSQL: %s (namespace: %s)", connInfo.Endpoint, namespace)

	// 2. Establish initial connection (with default postgres database)
	db, err := c.openPostgreSQLConnection(connInfo.DSN)
	if err != nil {
		return fmt.Errorf("failed to open connection: %w", err)
	}
	defer db.Close()

	// 3. Test basic connection
	if err := c.testPostgreSQLBasicConnection(ctx, db, connInfo.Endpoint); err != nil {
		return err
	}

	c.logger.Debugf("PostgreSQL connectivity test passed: %s", connInfo.Endpoint)

	return nil
}

// parsePostgreSQLConnectionInfo parses connection information from secret
func (c *Collector) parsePostgreSQLConnectionInfo(
	secret *corev1.Secret,
	namespace string,
) (*PostgreSQLConnectionInfo, error) {
	// Extract username
	username, err := decodeSecret(secret.Data, "username")
	if err != nil {
		return nil, fmt.Errorf("failed to get username: %w", err)
	}

	// Extract password
	password, err := decodeSecret(secret.Data, "password")
	if err != nil {
		return nil, fmt.Errorf("failed to get password: %w", err)
	}

	// Extract host
	host, err := decodeSecret(secret.Data, "host")
	if err != nil {
		return nil, fmt.Errorf("failed to get host: %w", err)
	}

	// Extract port
	port, err := decodeSecret(secret.Data, "port")
	if err != nil {
		return nil, fmt.Errorf("failed to get port: %w", err)
	}

	// Build full endpoint with K8s service domain
	fullEndpoint := c.buildFullEndpoint(host, port, namespace)

	// Build DSN (Data Source Name) for PostgreSQL
	// Format: postgresql://username:password@host:port/postgres?sslmode=disable&connect_timeout=30
	dsn := fmt.Sprintf("postgresql://%s:%s@%s/postgres?sslmode=disable&connect_timeout=%d",
		username, password, fullEndpoint, int(c.config.CheckTimeout.Seconds()))

	return &PostgreSQLConnectionInfo{
		Username: username,
		password: password,
		Host:     host,
		Port:     port,
		Endpoint: fullEndpoint,
		DSN:      dsn,
	}, nil
}

// openPostgreSQLConnection opens a PostgreSQL connection
func (c *Collector) openPostgreSQLConnection(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	// Set connection parameters
	db.SetConnMaxLifetime(c.config.CheckTimeout)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)

	return db, nil
}

// testPostgreSQLBasicConnection tests basic PostgreSQL connection
func (c *Collector) testPostgreSQLBasicConnection(
	ctx context.Context,
	db *sql.DB,
	endpoint string,
) error {
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping PostgreSQL: %w", err)
	}

	return nil
}
