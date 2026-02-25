package database

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	corev1 "k8s.io/api/core/v1"
)

// RedisConnectionInfo holds Redis connection information
type RedisConnectionInfo struct {
	Password string
	Host     string
	Port     string
	Endpoint string
	Address  string
}

// checkRedisConnectivity checks Redis database connectivity
func (c *Collector) checkRedisConnectivity(
	ctx context.Context,
	namespace, dbName string,
	secret *corev1.Secret,
) error {
	// 1. Parse connection information
	connInfo, err := c.parseRedisConnectionInfo(secret, namespace, dbName)
	if err != nil {
		return fmt.Errorf("failed to parse connection info: %w", err)
	}

	c.logger.Infof("Connecting to Redis: %s (namespace: %s)", connInfo.Endpoint, namespace)

	// 2. Establish connection
	client, err := c.openRedisConnection(connInfo)
	if err != nil {
		return fmt.Errorf("failed to open connection: %w", err)
	}
	defer client.Close()

	// 3. Test basic connection
	if err := c.testRedisBasicConnection(ctx, client, connInfo.Endpoint); err != nil {
		return err
	}

	// 4. Generate test key name
	testKey := fmt.Sprintf("test_key_%d", time.Now().Unix())

	// 5. Test key-level permissions
	if err := c.testRedisKeyPermissions(ctx, client, testKey); err != nil {
		return err
	}

	// 6. Cleanup test key (if not already deleted)
	if err := c.cleanupRedisTestKey(ctx, client, testKey); err != nil {
		c.logger.WithError(err).Warnf("Failed to cleanup test key: %s", testKey)
		// Do not return error as tests already passed
	}

	c.logger.Infof("Redis all permission tests passed: %s", connInfo.Endpoint)
	return nil
}

// parseRedisConnectionInfo parses connection information from secret
func (c *Collector) parseRedisConnectionInfo(secret *corev1.Secret, namespace, dbName string) (*RedisConnectionInfo, error) {
	// Extract password
	password, err := decodeSecret(secret.Data, "password")
	if err != nil {
		c.logger.WithError(err).Error("Failed to parse password")
		return nil, fmt.Errorf("failed to get password: %w", err)
	}

	// Redis default host and port
	host := fmt.Sprintf("%s-redis-redis", dbName)
	port := "6379"

	// Build full endpoint with K8s service domain
	fullEndpoint := c.buildFullEndpoint(host, port, namespace)

	return &RedisConnectionInfo{
		Password: password,
		Host:     host,
		Port:     port,
		Endpoint: fullEndpoint,
		Address:  fullEndpoint,
	}, nil
}

// openRedisConnection opens a Redis connection
func (c *Collector) openRedisConnection(connInfo *RedisConnectionInfo) (*redis.Client, error) {
	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr:         connInfo.Address,
		Password:     connInfo.Password,
		DB:           0,
		DialTimeout:  c.config.CheckTimeout,
		ReadTimeout:  c.config.CheckTimeout,
		WriteTimeout: c.config.CheckTimeout,
	})

	return client, nil
}

// testRedisBasicConnection tests basic Redis connection
func (c *Collector) testRedisBasicConnection(ctx context.Context, client *redis.Client, endpoint string) error {
	c.logger.Debug("Executing Ping test")
	if err := client.Ping(ctx).Err(); err != nil {
		c.logger.WithError(err).Errorf("Redis Ping failed: %s", endpoint)
		return fmt.Errorf("failed to ping Redis: %w", err)
	}

	c.logger.Infof("✓ Redis connection successful: %s", endpoint)
	return nil
}

// testRedisKeyPermissions tests key-level permissions
func (c *Collector) testRedisKeyPermissions(ctx context.Context, client *redis.Client, testKey string) error {
	// Test SET
	if err := c.testRedisSet(ctx, client, testKey); err != nil {
		return err
	}

	// Test GET
	if err := c.testRedisGet(ctx, client, testKey); err != nil {
		return err
	}

	// Test EXISTS
	if err := c.testRedisExists(ctx, client, testKey); err != nil {
		return err
	}

	// Test UPDATE (SET with new value)
	if err := c.testRedisUpdate(ctx, client, testKey); err != nil {
		return err
	}

	// Test DEL
	if err := c.testRedisDelete(ctx, client, testKey); err != nil {
		return err
	}

	return nil
}

// testRedisSet tests SET permission
func (c *Collector) testRedisSet(ctx context.Context, client *redis.Client, testKey string) error {
	c.logger.Debug("Testing SET permission")
	if err := client.Set(ctx, testKey, "test_value", 0).Err(); err != nil {
		c.logger.WithError(err).Error("SET failed")
		return fmt.Errorf("failed to set key: %w", err)
	}
	c.logger.Info("✓ SET permission OK")
	return nil
}

// testRedisGet tests GET permission
func (c *Collector) testRedisGet(ctx context.Context, client *redis.Client, testKey string) error {
	c.logger.Debug("Testing GET permission")
	val, err := client.Get(ctx, testKey).Result()
	if err != nil {
		c.logger.WithError(err).Error("GET failed")
		return fmt.Errorf("failed to get key: %w", err)
	}

	// Verify result
	if val != "test_value" {
		c.logger.Errorf("GET result incorrect: got %s, expected test_value", val)
		return fmt.Errorf("unexpected get result: got %s, expected test_value", val)
	}
	c.logger.Info("✓ GET permission OK")
	return nil
}

// testRedisExists tests EXISTS permission
func (c *Collector) testRedisExists(ctx context.Context, client *redis.Client, testKey string) error {
	c.logger.Debug("Testing EXISTS permission")
	exists, err := client.Exists(ctx, testKey).Result()
	if err != nil {
		c.logger.WithError(err).Error("EXISTS failed")
		return fmt.Errorf("failed to check key existence: %w", err)
	}

	if exists != 1 {
		c.logger.Errorf("EXISTS result incorrect: got %d, expected 1", exists)
		return fmt.Errorf("unexpected exists result: got %d, expected 1", exists)
	}
	c.logger.Info("✓ EXISTS permission OK")
	return nil
}

// testRedisUpdate tests UPDATE (SET with new value) permission
func (c *Collector) testRedisUpdate(ctx context.Context, client *redis.Client, testKey string) error {
	c.logger.Debug("Testing UPDATE permission")
	if err := client.Set(ctx, testKey, "updated_value", 0).Err(); err != nil {
		c.logger.WithError(err).Error("UPDATE (SET) failed")
		return fmt.Errorf("failed to update key: %w", err)
	}
	c.logger.Info("✓ UPDATE permission OK")
	return nil
}

// testRedisDelete tests DEL permission
func (c *Collector) testRedisDelete(ctx context.Context, client *redis.Client, testKey string) error {
	c.logger.Debug("Testing DEL permission")
	if err := client.Del(ctx, testKey).Err(); err != nil {
		c.logger.WithError(err).Error("DEL failed")
		return fmt.Errorf("failed to delete key: %w", err)
	}
	c.logger.Info("✓ DEL permission OK")
	return nil
}

// cleanupRedisTestKey cleans up the test key (idempotent operation)
func (c *Collector) cleanupRedisTestKey(ctx context.Context, client *redis.Client, testKey string) error {
	c.logger.Debugf("Cleaning up test key: %s", testKey)

	// Check if key exists first
	exists, err := client.Exists(ctx, testKey).Result()
	if err != nil {
		c.logger.WithError(err).Warnf("Failed to check key existence: %s", testKey)
		return fmt.Errorf("failed to check key existence: %w", err)
	}

	if exists == 0 {
		c.logger.Debugf("Test key already deleted: %s", testKey)
		return nil
	}

	// Delete the key
	if err := client.Del(ctx, testKey).Err(); err != nil {
		c.logger.WithError(err).Warnf("Failed to delete test key: %s", testKey)
		return fmt.Errorf("failed to delete test key: %w", err)
	}

	c.logger.Infof("✓ Test key cleaned up: %s", testKey)
	return nil
}
