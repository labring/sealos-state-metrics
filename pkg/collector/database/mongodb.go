package database

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	corev1 "k8s.io/api/core/v1"
)

// MongoDBConnectionInfo holds MongoDB connection information
type MongoDBConnectionInfo struct {
	Username string
	password string
	Host     string
	Port     string
	Endpoint string
	URI      string
}

// checkMongoDBConnectivity checks MongoDB database connectivity
func (c *Collector) checkMongoDBConnectivity(
	ctx context.Context,
	namespace, dbName string,
	secret *corev1.Secret,
) error {
	// 1. Parse connection information
	connInfo, err := c.parseMongoDBConnectionInfo(secret, namespace, dbName)
	if err != nil {
		return fmt.Errorf("failed to parse connection info: %w", err)
	}

	c.logger.Debugf("Connecting to MongoDB: %s (namespace: %s)", connInfo.Endpoint, namespace)

	// 2. Establish connection
	client, err := c.openMongoDBConnection(ctx, connInfo.URI)
	if err != nil {
		return fmt.Errorf("failed to open connection: %w", err)
	}

	defer func() {
		if err := client.Disconnect(ctx); err != nil {
			c.logger.WithError(err).Warn("Failed to disconnect from MongoDB")
		}
	}()

	// 3. Test basic connection
	if err := c.testMongoDBBasicConnection(ctx, client, connInfo.Endpoint); err != nil {
		return err
	}

	c.logger.Infof("MongoDB connectivity test passed: %s", connInfo.Endpoint)

	return nil
}

// parseMongoDBConnectionInfo parses connection information from secret
func (c *Collector) parseMongoDBConnectionInfo(
	secret *corev1.Secret,
	namespace, dbName string,
) (*MongoDBConnectionInfo, error) {
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

	// MongoDB default host and port
	host := dbName + "-mongodb"
	port := "27017"

	// Build full endpoint with K8s service domain
	fullEndpoint := c.buildFullEndpoint(host, port, namespace)

	// Build MongoDB URI
	// Format: mongodb://username:password@host:port
	uri := fmt.Sprintf("mongodb://%s:%s@%s", username, password, fullEndpoint)

	return &MongoDBConnectionInfo{
		Username: username,
		password: password,
		Host:     host,
		Port:     port,
		Endpoint: fullEndpoint,
		URI:      uri,
	}, nil
}

// openMongoDBConnection opens a MongoDB connection
func (c *Collector) openMongoDBConnection(ctx context.Context, uri string) (*mongo.Client, error) {
	// Set client options
	clientOptions := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(c.config.CheckTimeout).
		SetServerSelectionTimeout(c.config.CheckTimeout).
		SetSocketTimeout(c.config.CheckTimeout)

	// Connect to MongoDB
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		c.logger.WithError(err).Error("Failed to open MongoDB connection")
		return nil, err
	}

	return client, nil
}

// testMongoDBBasicConnection tests basic MongoDB connection
func (c *Collector) testMongoDBBasicConnection(
	ctx context.Context,
	client *mongo.Client,
	endpoint string,
) error {
	if err := client.Ping(ctx, nil); err != nil {
		c.logger.WithError(err).Errorf("MongoDB Ping failed: %s", endpoint)
		return fmt.Errorf("failed to ping MongoDB: %w", err)
	}

	return nil
}
