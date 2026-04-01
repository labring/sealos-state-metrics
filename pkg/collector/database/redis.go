package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
	corev1 "k8s.io/api/core/v1"
)

// RedisConnectionInfo holds Redis connection information
type RedisConnectionInfo struct {
	password string
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

	c.logger.Debugf("Connecting to Redis: %s (namespace: %s)", connInfo.Endpoint, namespace)

	// 2. Establish connection
	client, err := c.openRedisConnection(connInfo)
	if err != nil {
		return fmt.Errorf("failed to open connection: %w", err)
	}
	defer client.Close()

	// 3. Test basic connection
	if err := c.testRedisBasicConnection(ctx, client); err != nil {
		return err
	}

	c.logger.Debugf("Redis connectivity test passed: %s", connInfo.Endpoint)

	return nil
}

// parseRedisConnectionInfo parses connection information from secret
func (c *Collector) parseRedisConnectionInfo(
	secret *corev1.Secret,
	namespace, dbName string,
) (*RedisConnectionInfo, error) {
	// Extract password
	password, err := decodeSecret(secret.Data, "password")
	if err != nil {
		return nil, fmt.Errorf("failed to get password: %w", err)
	}

	// Extract endpoint from secret (same format as MySQL conn-credential)
	endpoint, err := decodeSecret(secret.Data, "endpoint")
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoint: %w", err)
	}

	// Parse host and port
	host := endpoint
	port := "6379" // Redis default port

	if strings.Contains(endpoint, ":") {
		parts := strings.Split(endpoint, ":")
		host = parts[0]
		port = parts[1]
	}

	// Build full endpoint with K8s service domain
	fullEndpoint := c.buildFullEndpoint(host, port, namespace)

	return &RedisConnectionInfo{
		password: password,
		Host:     host,
		Port:     port,
		Endpoint: fullEndpoint,
		Address:  fullEndpoint,
	}, nil
}

// openRedisConnection opens a Redis connection
//
//nolint:unparam // error return reserved for consistency with other database connectors
func (c *Collector) openRedisConnection(connInfo *RedisConnectionInfo) (*redis.Client, error) {
	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr:         connInfo.Address,
		Password:     connInfo.password,
		DB:           0,
		DialTimeout:  c.config.CheckTimeout,
		ReadTimeout:  c.config.CheckTimeout,
		WriteTimeout: c.config.CheckTimeout,
		PoolSize:     1,
		MinIdleConns: 0,
	})

	return client, nil
}

// testRedisBasicConnection tests basic Redis connection
func (c *Collector) testRedisBasicConnection(
	ctx context.Context,
	client *redis.Client,
) error {
	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("failed to ping Redis: %w", err)
	}

	return nil
}
