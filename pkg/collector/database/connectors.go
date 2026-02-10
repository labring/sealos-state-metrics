package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	corev1 "k8s.io/api/core/v1"
)

// decodeSecret decodes a base64 encoded secret value
func decodeSecret(data map[string][]byte, key string) (string, error) {
	encoded, ok := data[key]
	if !ok {
		return "", fmt.Errorf("key %s not found in secret", key)
	}

	// The data is already decoded by Kubernetes client
	return string(encoded), nil
}

// sanitizeIdentifier sanitizes a SQL identifier to prevent SQL injection
// It only allows alphanumeric characters, underscores, and hyphens
// This is used for database names, table names, etc.
func sanitizeIdentifier(identifier string) (string, error) {
	// Only allow alphanumeric, underscore, and hyphen
	re := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !re.MatchString(identifier) {
		return "", fmt.Errorf("invalid identifier: contains illegal characters")
	}

	// MySQL identifier length limit is 64 characters
	if len(identifier) > 64 {
		return "", fmt.Errorf("identifier too long: max 64 characters")
	}

	return identifier, nil
}

// quoteIdentifier quotes a SQL identifier for safe use in queries
// This is a defense-in-depth measure even after sanitization
func quoteIdentifier(identifier string, dbType string) string {
	switch dbType {
	case "mysql":
		// MySQL uses backticks
		return "`" + identifier + "`"
	case "postgres":
		// PostgreSQL uses double quotes
		return `"` + identifier + `"`
	default:
		return identifier
	}
}

// buildSafeDDL constructs a DDL statement with a sanitized and quoted identifier
// This function provides an additional layer of safety and makes the security measures explicit
// Returns the SQL statement and any error from sanitization
func buildSafeDDL(template string, identifier string, dbType string) (string, error) {
	// Sanitize first
	sanitized, err := sanitizeIdentifier(identifier)
	if err != nil {
		return "", fmt.Errorf("identifier sanitization failed: %w", err)
	}

	// Quote the identifier
	quoted := quoteIdentifier(sanitized, dbType)

	// Build the SQL - safe because identifier has been sanitized and quoted
	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	sql := fmt.Sprintf(template, quoted)

	return sql, nil
}

// checkMySQLConnectivity checks MySQL database connectivity
func (c *Collector) checkMySQLConnectivity(ctx context.Context, namespace, dbName string, secret *corev1.Secret) error {
	// Extract connection information from secret
	username, err := decodeSecret(secret.Data, "username")
	if err != nil {
		return fmt.Errorf("failed to get username: %w", err)
	}

	password, err := decodeSecret(secret.Data, "password")
	if err != nil {
		return fmt.Errorf("failed to get password: %w", err)
	}

	endpoint, err := decodeSecret(secret.Data, "endpoint")
	if err != nil {
		return fmt.Errorf("failed to get endpoint: %w", err)
	}

	// Build connection string
	// Format: username:password@tcp(host:port)/
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/", username, password, endpoint)

	// Connect to MySQL
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("failed to open MySQL connection: %w", err)
	}
	defer db.Close()

	// Set connection timeout
	db.SetConnMaxLifetime(c.config.CheckTimeout)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)

	// Ping database
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping MySQL: %w", err)
	}

	// Run validation commands
	testDBName := fmt.Sprintf("test_db_%d", time.Now().Unix())

	// SHOW DATABASES - No user input, completely safe static query
	if _, err := db.ExecContext(ctx, "SHOW DATABASES"); err != nil {
		return fmt.Errorf("failed to show databases: %w", err)
	}

	// CREATE DATABASE - Build safe DDL with sanitization and quoting
	// buildSafeDDL ensures the identifier is validated and properly quoted
	createDBQuery, err := buildSafeDDL("CREATE DATABASE IF NOT EXISTS %s", testDBName, "mysql")
	if err != nil {
		return fmt.Errorf("failed to build CREATE DATABASE query: %w", err)
	}
	if _, err := db.ExecContext(ctx, createDBQuery); err != nil {
		return fmt.Errorf("failed to create test database: %w", err)
	}

	// USE DATABASE - Build safe DDL with sanitization and quoting
	// buildSafeDDL ensures the identifier is validated and properly quoted
	useDBQuery, err := buildSafeDDL("USE %s", testDBName, "mysql")
	if err != nil {
		return fmt.Errorf("failed to build USE query: %w", err)
	}
	if _, err := db.ExecContext(ctx, useDBQuery); err != nil {
		return fmt.Errorf("failed to use test database: %w", err)
	}

	// CREATE TABLE - No user input, completely safe static query
	if _, err := db.ExecContext(ctx, "CREATE TABLE test_table (id INT)"); err != nil {
		return fmt.Errorf("failed to create test table: %w", err)
	}

	// INSERT - No user input, completely safe static query
	if _, err := db.ExecContext(ctx, "INSERT INTO test_table VALUES (1)"); err != nil {
		return fmt.Errorf("failed to insert test data: %w", err)
	}

	// SELECT - No user input, completely safe static query
	var id int
	if err := db.QueryRowContext(ctx, "SELECT * FROM test_table").Scan(&id); err != nil {
		return fmt.Errorf("failed to select test data: %w", err)
	}

	// DROP TABLE - No user input, completely safe static query
	if _, err := db.ExecContext(ctx, "DROP TABLE test_table"); err != nil {
		return fmt.Errorf("failed to drop test table: %w", err)
	}

	// DROP DATABASE - Build safe DDL with sanitization and quoting
	// buildSafeDDL ensures the identifier is validated and properly quoted
	dropDBQuery, err := buildSafeDDL("DROP DATABASE %s", testDBName, "mysql")
	if err != nil {
		return fmt.Errorf("failed to build DROP DATABASE query: %w", err)
	}
	if _, err := db.ExecContext(ctx, dropDBQuery); err != nil {
		return fmt.Errorf("failed to drop test database: %w", err)
	}

	return nil
}

// checkPostgreSQLConnectivity checks PostgreSQL database connectivity
func (c *Collector) checkPostgreSQLConnectivity(ctx context.Context, namespace, dbName string, secret *corev1.Secret) error {
	// Extract connection information from secret
	username, err := decodeSecret(secret.Data, "username")
	if err != nil {
		return fmt.Errorf("failed to get username: %w", err)
	}

	password, err := decodeSecret(secret.Data, "password")
	if err != nil {
		return fmt.Errorf("failed to get password: %w", err)
	}

	host, err := decodeSecret(secret.Data, "host")
	if err != nil {
		return fmt.Errorf("failed to get host: %w", err)
	}

	port, err := decodeSecret(secret.Data, "port")
	if err != nil {
		return fmt.Errorf("failed to get port: %w", err)
	}

	// Build connection string with proper URL encoding to prevent injection
	// Format: postgresql://username:password@host:port/postgres?sslmode=disable
	// Use url.UserPassword for safe encoding of credentials
	userInfo := url.UserPassword(username, password)

	// Construct the DSN safely
	dsn := fmt.Sprintf("postgresql://%s@%s.%s.svc:%s/postgres?sslmode=disable&connect_timeout=%d",
		userInfo.String(), host, namespace, port, int(c.config.CheckTimeout.Seconds()))

	// Connect to PostgreSQL
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("failed to open PostgreSQL connection: %w", err)
	}
	defer db.Close()

	// Set connection timeout
	db.SetConnMaxLifetime(c.config.CheckTimeout)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)

	// Ping database
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping PostgreSQL: %w", err)
	}

	// Run validation commands
	// List databases (equivalent to \l)
	if _, err := db.ExecContext(ctx, "SELECT datname FROM pg_database"); err != nil {
		return fmt.Errorf("failed to list databases: %w", err)
	}

	// CREATE TABLE - Safe query without user input
	if _, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS test_table (id SERIAL)"); err != nil {
		return fmt.Errorf("failed to create test table: %w", err)
	}

	// INSERT - Safe query
	if _, err := db.ExecContext(ctx, "INSERT INTO test_table DEFAULT VALUES"); err != nil {
		return fmt.Errorf("failed to insert test data: %w", err)
	}

	// SELECT - Safe query
	var id int
	if err := db.QueryRowContext(ctx, "SELECT * FROM test_table LIMIT 1").Scan(&id); err != nil {
		return fmt.Errorf("failed to select test data: %w", err)
	}

	// DROP TABLE - Safe query without user input
	if _, err := db.ExecContext(ctx, "DROP TABLE test_table"); err != nil {
		return fmt.Errorf("failed to drop test table: %w", err)
	}

	return nil
}

// checkMongoDBConnectivity checks MongoDB database connectivity
func (c *Collector) checkMongoDBConnectivity(ctx context.Context, namespace, dbName string, secret *corev1.Secret) error {
	// Extract connection information from secret
	username, err := decodeSecret(secret.Data, "username")
	if err != nil {
		return fmt.Errorf("failed to get username: %w", err)
	}

	password, err := decodeSecret(secret.Data, "password")
	if err != nil {
		return fmt.Errorf("failed to get password: %w", err)
	}

	// Build MongoDB service hostname
	// Format: dbName-mongodb.namespace.svc:27017
	host := fmt.Sprintf("%s-mongodb.%s.svc:27017", dbName, namespace)

	// Build connection string with proper URL encoding
	// MongoDB URI format: mongodb://username:password@host:port
	// Use url.UserPassword for safe credential encoding
	userInfo := url.UserPassword(username, password)
	uri := fmt.Sprintf("mongodb://%s@%s", userInfo.String(), host)

	// Set client options
	clientOptions := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(c.config.CheckTimeout).
		SetServerSelectionTimeout(c.config.CheckTimeout).
		SetSocketTimeout(c.config.CheckTimeout)

	// Connect to MongoDB
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	defer func() {
		if err := client.Disconnect(ctx); err != nil {
			c.logger.WithError(err).Warn("Failed to disconnect from MongoDB")
		}
	}()

	// Ping MongoDB
	if err := client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("failed to ping MongoDB: %w", err)
	}

	// Run validation commands
	// Use a safe database name (no user input in the name)
	testDB := client.Database("test_db")

	// Show databases (list database names)
	if _, err := client.ListDatabaseNames(ctx, map[string]interface{}{}); err != nil {
		return fmt.Errorf("failed to list databases: %w", err)
	}

	// Insert one document - MongoDB driver handles BSON encoding safely
	collection := testDB.Collection("test_collection")
	if _, err := collection.InsertOne(ctx, map[string]interface{}{"test": 1}); err != nil {
		return fmt.Errorf("failed to insert test document: %w", err)
	}

	// Find one document - Safe query using BSON
	var result map[string]interface{}
	if err := collection.FindOne(ctx, map[string]interface{}{"test": 1}).Decode(&result); err != nil {
		return fmt.Errorf("failed to find test document: %w", err)
	}

	// Drop collection
	if err := collection.Drop(ctx); err != nil {
		return fmt.Errorf("failed to drop test collection: %w", err)
	}

	return nil
}

// checkRedisConnectivity checks Redis database connectivity
func (c *Collector) checkRedisConnectivity(ctx context.Context, namespace, dbName string, secret *corev1.Secret) error {
	// Extract connection information from secret
	password, err := decodeSecret(secret.Data, "password")
	if err != nil {
		return fmt.Errorf("failed to get password: %w", err)
	}

	// Build Redis service hostname
	// Format: dbName-redis-redis.namespace.svc:6379
	addr := fmt.Sprintf("%s-redis-redis.%s.svc:6379", dbName, namespace)

	// Create Redis client
	// Redis client handles password encoding safely
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           0,
		DialTimeout:  c.config.CheckTimeout,
		ReadTimeout:  c.config.CheckTimeout,
		WriteTimeout: c.config.CheckTimeout,
	})
	defer rdb.Close()

	// Ping Redis
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("failed to ping Redis: %w", err)
	}

	// Run validation commands
	// Use timestamp to create unique key (no special characters)
	testKey := fmt.Sprintf("test_key_%d", time.Now().Unix())

	// SET - Redis client handles key/value encoding safely
	if err := rdb.Set(ctx, testKey, "hello", 0).Err(); err != nil {
		return fmt.Errorf("failed to set test key: %w", err)
	}

	// GET - Safe operation
	val, err := rdb.Get(ctx, testKey).Result()
	if err != nil {
		return fmt.Errorf("failed to get test key: %w", err)
	}
	if val != "hello" {
		return fmt.Errorf("unexpected value for test key: got %s, want hello", val)
	}

	// DEL - Safe operation
	if err := rdb.Del(ctx, testKey).Err(); err != nil {
		return fmt.Errorf("failed to delete test key: %w", err)
	}

	return nil
}
