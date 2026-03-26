package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	corev1 "k8s.io/api/core/v1"
)

// MySQLConnectionInfo holds MySQL connection information
type MySQLConnectionInfo struct {
	Username string
	password string
	Host     string
	Port     string
	Endpoint string
	DSN      string
}

// checkMySQLConnectivity checks MySQL database connectivity
func (c *Collector) checkMySQLConnectivity(
	ctx context.Context,
	secret *corev1.Secret,
) error {
	namespace := secret.Namespace

	// 1. Parse connection information
	connInfo, err := c.parseMySQLConnectionInfo(secret)
	if err != nil {
		return fmt.Errorf("failed to parse connection info: %w", err)
	}

	c.logger.Debugf("Connecting to MySQL: %s (namespace: %s)", connInfo.Endpoint, namespace)

	// 2. Establish initial connection (without specifying database)
	db, err := c.openMySQLConnection(connInfo.DSN)
	if err != nil {
		return fmt.Errorf("failed to open connection: %w", err)
	}
	defer db.Close()

	// 3. Test basic connection
	if err := c.testMySQLBasicConnection(ctx, db); err != nil {
		return err
	}

	c.logger.Debugf("MySQL connectivity test passed: %s", connInfo.Endpoint)

	return nil
}

// parseMySQLConnectionInfo parses connection information from secret
func (c *Collector) parseMySQLConnectionInfo(secret *corev1.Secret) (*MySQLConnectionInfo, error) {
	namespace := secret.Namespace

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

	// Extract endpoint
	endpoint, err := decodeSecret(secret.Data, "endpoint")
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoint: %w", err)
	}

	// Parse host and port
	host := endpoint
	port := "3306" // MySQL default port

	if strings.Contains(endpoint, ":") {
		parts := strings.Split(endpoint, ":")
		host = parts[0]
		port = parts[1]
	}

	// Build full endpoint with K8s service domain
	fullEndpoint := c.buildFullEndpoint(host, port, namespace)

	// Build DSN (Data Source Name)
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/", username, password, fullEndpoint)

	return &MySQLConnectionInfo{
		Username: username,
		password: password,
		Host:     host,
		Port:     port,
		Endpoint: fullEndpoint,
		DSN:      dsn,
	}, nil
}

// buildFullEndpoint builds full K8s service endpoint
func (c *Collector) buildFullEndpoint(host, port, namespace string) string {
	// If host doesn't contain domain suffix, add full K8s service domain
	if !strings.Contains(host, ".svc.cluster.local") {
		if strings.Contains(host, ".") {
			// Already contains partial domain, might be service-name.namespace format
			return fmt.Sprintf("%s.svc.cluster.local:%s", host, port)
		} else {
			// Only service name, need to add full domain
			return fmt.Sprintf("%s.%s.svc.cluster.local:%s", host, namespace, port)
		}
	}
	// Already full domain
	return fmt.Sprintf("%s:%s", host, port)
}

// openMySQLConnection opens a MySQL connection
func (c *Collector) openMySQLConnection(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}

	// Set connection parameters
	db.SetConnMaxLifetime(c.config.CheckTimeout)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)

	return db, nil
}

// testMySQLBasicConnection tests basic MySQL connection
func (c *Collector) testMySQLBasicConnection(
	ctx context.Context,
	db *sql.DB,
) error {
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping MySQL: %w", err)
	}

	return nil
}
