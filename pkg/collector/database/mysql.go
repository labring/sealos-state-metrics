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

	// 1. 解析连接信息
	connInfo, err := c.parseMySQLConnectionInfo(secret)
	if err != nil {
		return fmt.Errorf("failed to parse connection info: %w", err)
	}

	c.logger.Infof("开始连接 MySQL: %s (命名空间: %s)", connInfo.Endpoint, namespace)

	// 2. 建立初始连接（不指定数据库）
	db, err := c.openMySQLConnection(connInfo.DSN)
	if err != nil {
		return fmt.Errorf("failed to open connection: %w", err)
	}
	defer db.Close()

	// 3. 测试基础连接
	if err := c.testMySQLBasicConnection(ctx, db, connInfo.Endpoint); err != nil {
		return err
	}

	// 4. 生成测试数据库名
	testDBName := fmt.Sprintf("test_db_%d", time.Now().Unix())

	// 5. 测试数据库级别权限
	if err := c.testMySQLDatabasePermissions(ctx, db, testDBName); err != nil {
		return err
	}

	// 6. 重新连接到测试数据库
	dbWithTestDB, err := c.reconnectToDatabase(connInfo, testDBName)
	if err != nil {
		c.cleanupMySQLTestDatabase(ctx, db, testDBName)
		return fmt.Errorf("failed to reconnect to test database: %w", err)
	}
	defer dbWithTestDB.Close()

	// 7. 测试表级别权限
	if err := c.testMySQLTablePermissions(ctx, dbWithTestDB); err != nil {
		c.cleanupMySQLTestDatabase(ctx, db, testDBName)
		return err
	}

	// 8. 清理测试数据库
	if err := c.cleanupMySQLTestDatabase(ctx, db, testDBName); err != nil {
		c.logger.WithError(err).Warnf("清理测试数据库失败: %s", testDBName)
		// 不返回错误，因为测试已经通过
	}

	c.logger.Infof("🎉 MySQL 所有权限测试通过: %s", connInfo.Endpoint)
	return nil
}

// parseMySQLConnectionInfo parses connection information from secret
func (c *Collector) parseMySQLConnectionInfo(secret *corev1.Secret) (*MySQLConnectionInfo, error) {
	namespace := secret.Namespace

	// Extract username
	username, err := decodeSecret(secret.Data, "username")
	if err != nil {
		c.logger.WithError(err).Error("解析用户名失败")
		return nil, fmt.Errorf("failed to get username: %w", err)
	}

	// Extract password
	password, err := decodeSecret(secret.Data, "password")
	if err != nil {
		c.logger.WithError(err).Error("解析密码失败")
		return nil, fmt.Errorf("failed to get password: %w", err)
	}

	// Extract endpoint
	endpoint, err := decodeSecret(secret.Data, "endpoint")
	if err != nil {
		c.logger.WithError(err).Error("解析连接地址失败")
		return nil, fmt.Errorf("failed to get endpoint: %w", err)
	}

	// Parse host and port
	host := endpoint
	port := "3306" // MySQL 默认端口

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
	// 如果 host 不包含域名后缀，则添加完整的 K8s 服务域名
	if !strings.Contains(host, ".svc.cluster.local") {
		if strings.Contains(host, ".") {
			// 已经包含部分域名，可能是 service-name.namespace 格式
			return fmt.Sprintf("%s.svc.cluster.local:%s", host, port)
		} else {
			// 只有服务名，需要添加完整域名
			return fmt.Sprintf("%s.%s.svc.cluster.local:%s", host, namespace, port)
		}
	}
	// 已经是完整域名
	return fmt.Sprintf("%s:%s", host, port)
}

// openMySQLConnection opens a MySQL connection
func (c *Collector) openMySQLConnection(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		c.logger.WithError(err).Error("打开 MySQL 连接失败")
		return nil, err
	}

	// Set connection parameters
	db.SetConnMaxLifetime(c.config.CheckTimeout)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)

	return db, nil
}

// testMySQLBasicConnection tests basic MySQL connection
func (c *Collector) testMySQLBasicConnection(ctx context.Context, db *sql.DB, endpoint string) error {
	c.logger.Debug("执行 Ping 测试")
	if err := db.PingContext(ctx); err != nil {
		c.logger.WithError(err).Errorf("MySQL Ping 失败: %s", endpoint)
		return fmt.Errorf("failed to ping MySQL: %w", err)
	}

	c.logger.Infof("✓ MySQL 连接成功: %s", endpoint)
	return nil
}

// testMySQLDatabasePermissions tests database-level permissions
func (c *Collector) testMySQLDatabasePermissions(ctx context.Context, db *sql.DB, testDBName string) error {
	// Test SHOW DATABASES
	c.logger.Debug("测试 SHOW DATABASES 权限")
	if _, err := db.ExecContext(ctx, "SHOW DATABASES"); err != nil {
		c.logger.WithError(err).Error("SHOW DATABASES 执行失败")
		return fmt.Errorf("failed to show databases: %w", err)
	}
	c.logger.Info("✓ SHOW DATABASES 权限正常")

	// Test CREATE DATABASE
	c.logger.Debugf("测试 CREATE DATABASE 权限: %s", testDBName)
	createDBQuery, err := buildSafeDDL("CREATE DATABASE IF NOT EXISTS %s", testDBName, "mysql")
	if err != nil {
		c.logger.WithError(err).Error("构建 CREATE DATABASE 语句失败")
		return fmt.Errorf("failed to build CREATE DATABASE query: %w", err)
	}

	if _, err := db.ExecContext(ctx, createDBQuery); err != nil {
		c.logger.WithError(err).Errorf("创建测试数据库失败: %s", testDBName)
		return fmt.Errorf("failed to create test database: %w", err)
	}
	c.logger.Infof("✓ CREATE DATABASE 权限正常 (创建了 %s)", testDBName)

	return nil
}

// reconnectToDatabase reconnects to a specific database
func (c *Collector) reconnectToDatabase(connInfo *MySQLConnectionInfo, dbName string) (*sql.DB, error) {
	// Build DSN with database name
	dsnWithDB := fmt.Sprintf("%s:%s@tcp(%s)/%s",
		connInfo.Username,
		connInfo.Password,
		connInfo.Endpoint,
		dbName,
	)

	c.logger.Debugf("重新连接到数据库: %s", dbName)
	db, err := c.openMySQLConnection(dsnWithDB)
	if err != nil {
		c.logger.WithError(err).Errorf("重新连接失败: %s", dbName)
		return nil, err
	}

	// Test connection
	if err := db.Ping(); err != nil {
		c.logger.WithError(err).Errorf("测试数据库 Ping 失败: %s", dbName)
		db.Close()
		return nil, fmt.Errorf("failed to ping test database: %w", err)
	}

	c.logger.Debugf("✓ 已连接到测试数据库: %s", dbName)
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
	c.logger.Debug("测试 CREATE TABLE 权限")
	createTableQuery := fmt.Sprintf("CREATE TABLE `%s` (id INT PRIMARY KEY, name VARCHAR(50))", tableName)
	if _, err := db.ExecContext(ctx, createTableQuery); err != nil {
		c.logger.WithError(err).Error("CREATE TABLE 失败")
		return fmt.Errorf("failed to create table: %w", err)
	}
	c.logger.Info("✓ CREATE TABLE 权限正常")
	return nil
}

// testMySQLInsert tests INSERT permission
func (c *Collector) testMySQLInsert(ctx context.Context, db *sql.DB, tableName string) error {
	c.logger.Debug("测试 INSERT 权限")
	insertQuery := fmt.Sprintf("INSERT INTO `%s` (id, name) VALUES (?, ?)", tableName)
	if _, err := db.ExecContext(ctx, insertQuery, 1, "test"); err != nil {
		c.logger.WithError(err).Error("INSERT 失败")
		return fmt.Errorf("failed to insert data: %w", err)
	}
	c.logger.Info("✓ INSERT 权限正常")
	return nil
}

// testMySQLSelect tests SELECT permission
func (c *Collector) testMySQLSelect(ctx context.Context, db *sql.DB, tableName string) error {
	c.logger.Debug("测试 SELECT 权限")
	var id int
	var name string
	selectQuery := fmt.Sprintf("SELECT id, name FROM `%s` WHERE id = ?", tableName)
	if err := db.QueryRowContext(ctx, selectQuery, 1).Scan(&id, &name); err != nil {
		c.logger.WithError(err).Error("SELECT 失败")
		return fmt.Errorf("failed to select data: %w", err)
	}

	if id != 1 || name != "test" {
		c.logger.Errorf("SELECT 结果不正确: id=%d, name=%s", id, name)
		return fmt.Errorf("unexpected select result: id=%d, name=%s", id, name)
	}
	c.logger.Info("✓ SELECT 权限正常")
	return nil
}

// testMySQLUpdate tests UPDATE permission
func (c *Collector) testMySQLUpdate(ctx context.Context, db *sql.DB, tableName string) error {
	c.logger.Debug("测试 UPDATE 权限")
	updateQuery := fmt.Sprintf("UPDATE `%s` SET name = ? WHERE id = ?", tableName)
	if _, err := db.ExecContext(ctx, updateQuery, "updated", 1); err != nil {
		c.logger.WithError(err).Error("UPDATE 失败")
		return fmt.Errorf("failed to update data: %w", err)
	}
	c.logger.Info("✓ UPDATE 权限正常")
	return nil
}

// testMySQLDelete tests DELETE permission
func (c *Collector) testMySQLDelete(ctx context.Context, db *sql.DB, tableName string) error {
	c.logger.Debug("测试 DELETE 权限")
	deleteQuery := fmt.Sprintf("DELETE FROM `%s` WHERE id = ?", tableName)
	if _, err := db.ExecContext(ctx, deleteQuery, 1); err != nil {
		c.logger.WithError(err).Error("DELETE 失败")
		return fmt.Errorf("failed to delete data: %w", err)
	}
	c.logger.Info("✓ DELETE 权限正常")
	return nil
}

// testMySQLDropTable tests DROP TABLE permission
func (c *Collector) testMySQLDropTable(ctx context.Context, db *sql.DB, tableName string) error {
	c.logger.Debug("测试 DROP TABLE 权限")
	dropTableQuery := fmt.Sprintf("DROP TABLE IF EXISTS `%s`", tableName)
	if _, err := db.ExecContext(ctx, dropTableQuery); err != nil {
		c.logger.WithError(err).Error("DROP TABLE 失败")
		return fmt.Errorf("failed to drop table: %w", err)
	}
	c.logger.Info("✓ DROP TABLE 权限正常")
	return nil
}

// cleanupMySQLTestDatabase cleans up the test database
func (c *Collector) cleanupMySQLTestDatabase(ctx context.Context, db *sql.DB, testDBName string) error {
	c.logger.Debugf("清理测试数据库: %s", testDBName)

	dropDBQuery, err := buildSafeDDL("DROP DATABASE IF EXISTS %s", testDBName, "mysql")
	if err != nil {
		c.logger.WithError(err).Error("构建 DROP DATABASE 语句失败")
		return fmt.Errorf("failed to build DROP DATABASE query: %w", err)
	}

	if _, err := db.ExecContext(ctx, dropDBQuery); err != nil {
		c.logger.WithError(err).Warnf("删除测试数据库失败: %s", testDBName)
		return fmt.Errorf("failed to drop test database: %w", err)
	}

	c.logger.Infof("✓ 测试数据库已清理: %s", testDBName)
	return nil
}
