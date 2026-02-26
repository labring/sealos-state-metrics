package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	corev1 "k8s.io/api/core/v1"
)

// MySQLConnectionInfo holds MySQL connection information
type MySQLConnectionInfo struct {
	Username string
	Password string
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
	if err := c.testMySQLBasicConnection(ctx, db, connInfo.Endpoint); err != nil {
		return err
	}

	// 4. Generate test database name
	testDBName := fmt.Sprintf("test_db_%d", time.Now().Unix())

	// 5. Test database-level permissions
	if err := c.testMySQLDatabasePermissions(ctx, db, testDBName); err != nil {
		return err
	}

	// 6. Reconnect to test database
	dbWithTestDB, err := c.reconnectToDatabase(connInfo, testDBName)
	if err != nil {
		_ = c.cleanupMySQLTestDatabase(ctx, db, testDBName) //nolint:errcheck // Cleanup error is non-critical
		return fmt.Errorf("failed to reconnect to test database: %w", err)
	}
	defer dbWithTestDB.Close()

	// 7. Test table-level permissions
	if err := c.testMySQLTablePermissions(ctx, dbWithTestDB); err != nil {
		_ = c.cleanupMySQLTestDatabase(ctx, db, testDBName) //nolint:errcheck // Cleanup error is non-critical
		return err
	}

	// 8. Cleanup test database
	if err := c.cleanupMySQLTestDatabase(ctx, db, testDBName); err != nil {
		c.logger.WithError(err).Warnf("Failed to cleanup test database: %s", testDBName)
		// Do not return error as tests already passed
	}

	c.logger.Infof("MySQL all permission tests passed: %s", connInfo.Endpoint)

	return nil
}

// parseMySQLConnectionInfo parses connection information from secret
func (c *Collector) parseMySQLConnectionInfo(secret *corev1.Secret) (*MySQLConnectionInfo, error) {
	namespace := secret.Namespace

	// Extract username
	username, err := decodeSecret(secret.Data, "username")
	if err != nil {
		c.logger.WithError(err).Error("Failed to parse username")
		return nil, fmt.Errorf("failed to get username: %w", err)
	}

	// Extract password
	password, err := decodeSecret(secret.Data, "password")
	if err != nil {
		c.logger.WithError(err).Error("Failed to parse password")
		return nil, fmt.Errorf("failed to get password: %w", err)
	}

	// Extract endpoint
	endpoint, err := decodeSecret(secret.Data, "endpoint")
	if err != nil {
		c.logger.WithError(err).Error("Failed to parse endpoint")
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
		Password: password,
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
		c.logger.WithError(err).Error("Failed to open MySQL connection")
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
	endpoint string,
) error {
	if err := db.PingContext(ctx); err != nil {
		c.logger.WithError(err).Errorf("MySQL Ping failed: %s", endpoint)
		return fmt.Errorf("failed to ping MySQL: %w", err)
	}

	return nil
}

// testMySQLDatabasePermissions tests database-level permissions
func (c *Collector) testMySQLDatabasePermissions(
	ctx context.Context,
	db *sql.DB,
	testDBName string,
) error {
	// Test SHOW DATABASES
	if _, err := db.ExecContext(ctx, "SHOW DATABASES"); err != nil {
		c.logger.WithError(err).Error("SHOW DATABASES execution failed")
		return fmt.Errorf("failed to show databases: %w", err)
	}

	// Test CREATE DATABASE
	createDBQuery, err := buildSafeDDL("CREATE DATABASE IF NOT EXISTS %s", testDBName, "mysql")
	if err != nil {
		c.logger.WithError(err).Error("Failed to build CREATE DATABASE statement")
		return fmt.Errorf("failed to build CREATE DATABASE query: %w", err)
	}

	if _, err := db.ExecContext(ctx, createDBQuery); err != nil {
		c.logger.WithError(err).Errorf("Failed to create test database: %s", testDBName)
		return fmt.Errorf("failed to create test database: %w", err)
	}

	return nil
}

// reconnectToDatabase reconnects to a specific database
func (c *Collector) reconnectToDatabase(
	connInfo *MySQLConnectionInfo,
	dbName string,
) (*sql.DB, error) {
	// Build DSN with database name
	dsnWithDB := fmt.Sprintf("%s:%s@tcp(%s)/%s",
		connInfo.Username,
		connInfo.Password,
		connInfo.Endpoint,
		dbName,
	)

	c.logger.Debugf("Reconnecting to database: %s", dbName)

	db, err := c.openMySQLConnection(dsnWithDB)
	if err != nil {
		c.logger.WithError(err).Errorf("Failed to reconnect: %s", dbName)
		return nil, err
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), c.config.CheckTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		c.logger.WithError(err).Errorf("Test database Ping failed: %s", dbName)
		db.Close()
		return nil, fmt.Errorf("failed to ping test database: %w", err)
	}

	c.logger.Debugf("✓ Connected to test database: %s", dbName)

	return db, nil
}

// testMySQLTablePermissions tests table-level permissions
func (c *Collector) testMySQLTablePermissions(ctx context.Context, db *sql.DB) error {
	testTableName := "test_table"

	// Test CREATE TABLE
	if err := c.testMySQLCreateTable(ctx, db, testTableName); err != nil {
		return err
	}

	// Test INSERT
	if err := c.testMySQLInsert(ctx, db, testTableName); err != nil {
		return err
	}

	// Test SELECT
	if err := c.testMySQLSelect(ctx, db, testTableName); err != nil {
		return err
	}

	// Test UPDATE
	if err := c.testMySQLUpdate(ctx, db, testTableName); err != nil {
		return err
	}

	// Test DELETE
	if err := c.testMySQLDelete(ctx, db, testTableName); err != nil {
		return err
	}

	// Test DROP TABLE
	if err := c.testMySQLDropTable(ctx, db, testTableName); err != nil {
		return err
	}

	return nil
}

// testMySQLCreateTable tests CREATE TABLE permission
func (c *Collector) testMySQLCreateTable(ctx context.Context, db *sql.DB, tableName string) error {
	// tableName is a constant "test_table", not user input
	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	createTableQuery := fmt.Sprintf(
		"CREATE TABLE `%s` (id INT PRIMARY KEY, name VARCHAR(50))",
		tableName,
	)
	if _, err := db.ExecContext(ctx, createTableQuery); err != nil {
		c.logger.WithError(err).Error("CREATE TABLE failed")
		return fmt.Errorf("failed to create table: %w", err)
	}

	return nil
}

// testMySQLInsert tests INSERT permission
func (c *Collector) testMySQLInsert(ctx context.Context, db *sql.DB, tableName string) error {
	// tableName is a constant "test_table", not user input
	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	//nolint:gosec // tableName is a constant, not user input
	insertQuery := fmt.Sprintf("INSERT INTO `%s` (id, name) VALUES (?, ?)", tableName)
	if _, err := db.ExecContext(ctx, insertQuery, 1, "test"); err != nil {
		c.logger.WithError(err).Error("INSERT failed")
		return fmt.Errorf("failed to insert data: %w", err)
	}

	return nil
}

// testMySQLSelect tests SELECT permission
func (c *Collector) testMySQLSelect(ctx context.Context, db *sql.DB, tableName string) error {
	var (
		id   int
		name string
	)
	// tableName is a constant "test_table", not user input
	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	//nolint:gosec // tableName is a constant, not user input
	selectQuery := fmt.Sprintf("SELECT id, name FROM `%s` WHERE id = ?", tableName)
	if err := db.QueryRowContext(ctx, selectQuery, 1).Scan(&id, &name); err != nil {
		c.logger.WithError(err).Error("SELECT failed")
		return fmt.Errorf("failed to select data: %w", err)
	}

	if id != 1 || name != "test" {
		c.logger.Errorf("SELECT result incorrect: id=%d, name=%s", id, name)
		return fmt.Errorf("unexpected select result: id=%d, name=%s", id, name)
	}

	return nil
}

// testMySQLUpdate tests UPDATE permission
func (c *Collector) testMySQLUpdate(ctx context.Context, db *sql.DB, tableName string) error {
	// tableName is a constant "test_table", not user input
	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	//nolint:gosec // tableName is a constant, not user input
	updateQuery := fmt.Sprintf("UPDATE `%s` SET name = ? WHERE id = ?", tableName)
	if _, err := db.ExecContext(ctx, updateQuery, "updated", 1); err != nil {
		c.logger.WithError(err).Error("UPDATE failed")
		return fmt.Errorf("failed to update data: %w", err)
	}

	return nil
}

// testMySQLDelete tests DELETE permission
func (c *Collector) testMySQLDelete(ctx context.Context, db *sql.DB, tableName string) error {
	// tableName is a constant "test_table", not user input
	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	//nolint:gosec // tableName is a constant, not user input
	deleteQuery := fmt.Sprintf("DELETE FROM `%s` WHERE id = ?", tableName)
	if _, err := db.ExecContext(ctx, deleteQuery, 1); err != nil {
		c.logger.WithError(err).Error("DELETE failed")
		return fmt.Errorf("failed to delete data: %w", err)
	}

	return nil
}

// testMySQLDropTable tests DROP TABLE permission
func (c *Collector) testMySQLDropTable(ctx context.Context, db *sql.DB, tableName string) error {
	// tableName is a constant "test_table", not user input
	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	dropTableQuery := fmt.Sprintf("DROP TABLE IF EXISTS `%s`", tableName)
	if _, err := db.ExecContext(ctx, dropTableQuery); err != nil {
		c.logger.WithError(err).Error("DROP TABLE failed")
		return fmt.Errorf("failed to drop table: %w", err)
	}

	return nil
}

// cleanupMySQLTestDatabase cleans up the test database
func (c *Collector) cleanupMySQLTestDatabase(
	ctx context.Context,
	db *sql.DB,
	testDBName string,
) error {
	c.logger.Debugf("Cleaning up test database: %s", testDBName)

	dropDBQuery, err := buildSafeDDL("DROP DATABASE IF EXISTS %s", testDBName, "mysql")
	if err != nil {
		c.logger.WithError(err).Error("Failed to build DROP DATABASE statement")
		return fmt.Errorf("failed to build DROP DATABASE query: %w", err)
	}

	if _, err := db.ExecContext(ctx, dropDBQuery); err != nil {
		c.logger.WithError(err).Warnf("Failed to drop test database: %s", testDBName)
		return fmt.Errorf("failed to drop test database: %w", err)
	}

	c.logger.Debugf("Test database cleaned up: %s", testDBName)

	return nil
}
