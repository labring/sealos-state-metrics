package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
	corev1 "k8s.io/api/core/v1"
)

// PostgreSQLConnectionInfo holds PostgreSQL connection information
type PostgreSQLConnectionInfo struct {
	Username string
	Password string
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

	// 4. Generate test database name
	testDBName := fmt.Sprintf("test_db_%d", time.Now().Unix())

	// 5. Test database-level permissions
	if err := c.testPostgreSQLDatabasePermissions(ctx, db, testDBName); err != nil {
		return err
	}

	// 6. Reconnect to test database
	dbWithTestDB, err := c.reconnectToPostgreSQLDatabase(connInfo, testDBName)
	if err != nil {
		_ = c.cleanupPostgreSQLTestDatabase(ctx, db, testDBName)
		return fmt.Errorf("failed to reconnect to test database: %w", err)
	}
	defer dbWithTestDB.Close()

	// 7. Test table-level permissions
	if err := c.testPostgreSQLTablePermissions(ctx, dbWithTestDB); err != nil {
		_ = c.cleanupPostgreSQLTestDatabase(ctx, db, testDBName)
		return err
	}

	// 8. Cleanup test database
	if err := c.cleanupPostgreSQLTestDatabase(ctx, db, testDBName); err != nil {
		c.logger.WithError(err).Warnf("Failed to cleanup test database: %s", testDBName)
		// Do not return error as tests already passed
	}

	c.logger.Infof("PostgreSQL all permission tests passed: %s", connInfo.Endpoint)

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
		c.logger.WithError(err).Error("Failed to parse username")
		return nil, fmt.Errorf("failed to get username: %w", err)
	}

	// Extract password
	password, err := decodeSecret(secret.Data, "password")
	if err != nil {
		c.logger.WithError(err).Error("Failed to parse password")
		return nil, fmt.Errorf("failed to get password: %w", err)
	}

	// Extract host
	host, err := decodeSecret(secret.Data, "host")
	if err != nil {
		c.logger.WithError(err).Error("Failed to parse host")
		return nil, fmt.Errorf("failed to get host: %w", err)
	}

	// Extract port
	port, err := decodeSecret(secret.Data, "port")
	if err != nil {
		c.logger.WithError(err).Error("Failed to parse port")
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
		Password: password,
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
		c.logger.WithError(err).Error("Failed to open PostgreSQL connection")
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
		c.logger.WithError(err).Errorf("PostgreSQL Ping failed: %s", endpoint)
		return fmt.Errorf("failed to ping PostgreSQL: %w", err)
	}

	return nil
}

// testPostgreSQLDatabasePermissions tests database-level permissions
func (c *Collector) testPostgreSQLDatabasePermissions(
	ctx context.Context,
	db *sql.DB,
	testDBName string,
) error {
	// Test LIST DATABASES
	if _, err := db.ExecContext(ctx, "SELECT datname FROM pg_database"); err != nil {
		c.logger.WithError(err).Error("LIST DATABASES execution failed")
		return fmt.Errorf("failed to list databases: %w", err)
	}

	// Test CREATE DATABASE
	createDBQuery, err := buildSafeDDL("CREATE DATABASE %s", testDBName, "postgres")
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

// reconnectToPostgreSQLDatabase reconnects to a specific database
func (c *Collector) reconnectToPostgreSQLDatabase(
	connInfo *PostgreSQLConnectionInfo,
	dbName string,
) (*sql.DB, error) {
	// Build DSN with database name
	dsnWithDB := fmt.Sprintf("postgresql://%s:%s@%s/%s?sslmode=disable&connect_timeout=%d",
		connInfo.Username,
		connInfo.Password,
		connInfo.Endpoint,
		dbName,
		int(c.config.CheckTimeout.Seconds()),
	)

	c.logger.Debugf("Reconnecting to database: %s", dbName)

	db, err := c.openPostgreSQLConnection(dsnWithDB)
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

// testPostgreSQLTablePermissions tests table-level permissions
func (c *Collector) testPostgreSQLTablePermissions(ctx context.Context, db *sql.DB) error {
	testTableName := "test_table"

	// Test CREATE TABLE
	if err := c.testPostgreSQLCreateTable(ctx, db, testTableName); err != nil {
		return err
	}

	// Test INSERT
	if err := c.testPostgreSQLInsert(ctx, db, testTableName); err != nil {
		return err
	}

	// Test SELECT
	if err := c.testPostgreSQLSelect(ctx, db, testTableName); err != nil {
		return err
	}

	// Test UPDATE
	if err := c.testPostgreSQLUpdate(ctx, db, testTableName); err != nil {
		return err
	}

	// Test DELETE
	if err := c.testPostgreSQLDelete(ctx, db, testTableName); err != nil {
		return err
	}

	// Test DROP TABLE
	if err := c.testPostgreSQLDropTable(ctx, db, testTableName); err != nil {
		return err
	}

	return nil
}

// testPostgreSQLCreateTable tests CREATE TABLE permission
func (c *Collector) testPostgreSQLCreateTable(
	ctx context.Context,
	db *sql.DB,
	tableName string,
) error {
	// tableName is a constant "test_table", not user input
	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	createTableQuery := fmt.Sprintf(
		"CREATE TABLE \"%s\" (id SERIAL PRIMARY KEY, name VARCHAR(50))",
		tableName,
	)
	if _, err := db.ExecContext(ctx, createTableQuery); err != nil {
		c.logger.WithError(err).Error("CREATE TABLE failed")
		return fmt.Errorf("failed to create table: %w", err)
	}

	return nil
}

// testPostgreSQLInsert tests INSERT permission
func (c *Collector) testPostgreSQLInsert(ctx context.Context, db *sql.DB, tableName string) error {
	// tableName is a constant "test_table", not user input
	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	//nolint:gosec // tableName is a constant, not user input
	insertQuery := fmt.Sprintf("INSERT INTO \"%s\" (name) VALUES ($1)", tableName)
	if _, err := db.ExecContext(ctx, insertQuery, "test"); err != nil {
		c.logger.WithError(err).Error("INSERT failed")
		return fmt.Errorf("failed to insert data: %w", err)
	}

	return nil
}

// testPostgreSQLSelect tests SELECT permission
func (c *Collector) testPostgreSQLSelect(ctx context.Context, db *sql.DB, tableName string) error {
	var (
		id   int
		name string
	)
	// tableName is a constant "test_table", not user input
	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	//nolint:gosec // tableName is a constant, not user input
	selectQuery := fmt.Sprintf("SELECT id, name FROM \"%s\" WHERE name = $1", tableName)
	if err := db.QueryRowContext(ctx, selectQuery, "test").Scan(&id, &name); err != nil {
		c.logger.WithError(err).Error("SELECT failed")
		return fmt.Errorf("failed to select data: %w", err)
	}

	if name != "test" {
		c.logger.Errorf("SELECT result incorrect: id=%d, name=%s", id, name)
		return fmt.Errorf("unexpected select result: id=%d, name=%s", id, name)
	}

	return nil
}

// testPostgreSQLUpdate tests UPDATE permission
func (c *Collector) testPostgreSQLUpdate(ctx context.Context, db *sql.DB, tableName string) error {
	// tableName is a constant "test_table", not user input
	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	//nolint:gosec // tableName is a constant, not user input
	updateQuery := fmt.Sprintf("UPDATE \"%s\" SET name = $1 WHERE name = $2", tableName)
	if _, err := db.ExecContext(ctx, updateQuery, "updated", "test"); err != nil {
		c.logger.WithError(err).Error("UPDATE failed")
		return fmt.Errorf("failed to update data: %w", err)
	}

	return nil
}

// testPostgreSQLDelete tests DELETE permission
func (c *Collector) testPostgreSQLDelete(ctx context.Context, db *sql.DB, tableName string) error {
	// tableName is a constant "test_table", not user input
	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	//nolint:gosec // tableName is a constant, not user input
	deleteQuery := fmt.Sprintf("DELETE FROM \"%s\" WHERE name = $1", tableName)
	if _, err := db.ExecContext(ctx, deleteQuery, "updated"); err != nil {
		c.logger.WithError(err).Error("DELETE failed")
		return fmt.Errorf("failed to delete data: %w", err)
	}

	return nil
}

// testPostgreSQLDropTable tests DROP TABLE permission
func (c *Collector) testPostgreSQLDropTable(
	ctx context.Context,
	db *sql.DB,
	tableName string,
) error {
	// tableName is a constant "test_table", not user input
	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	dropTableQuery := fmt.Sprintf("DROP TABLE IF EXISTS \"%s\"", tableName)
	if _, err := db.ExecContext(ctx, dropTableQuery); err != nil {
		c.logger.WithError(err).Error("DROP TABLE failed")
		return fmt.Errorf("failed to drop table: %w", err)
	}

	return nil
}

// cleanupPostgreSQLTestDatabase cleans up the test database
func (c *Collector) cleanupPostgreSQLTestDatabase(
	ctx context.Context,
	db *sql.DB,
	testDBName string,
) error {
	c.logger.Debugf("Cleaning up test database: %s", testDBName)

	dropDBQuery, err := buildSafeDDL("DROP DATABASE IF EXISTS %s", testDBName, "postgres")
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
