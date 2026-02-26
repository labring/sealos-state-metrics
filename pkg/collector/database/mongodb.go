package database

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	corev1 "k8s.io/api/core/v1"
)

// MongoDBConnectionInfo holds MongoDB connection information
type MongoDBConnectionInfo struct {
	Username string
	Password string
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

	// 4. Generate test database name
	testDBName := fmt.Sprintf("test_db_%d", time.Now().Unix())

	// 5. Test database-level permissions
	if err := c.testMongoDBDatabasePermissions(ctx, client, testDBName); err != nil {
		return err
	}

	// 6. Test collection-level permissions
	if err := c.testMongoDBCollectionPermissions(ctx, client, testDBName); err != nil {
		_ = c.cleanupMongoDBTestDatabase(ctx, client, testDBName)
		return err
	}

	// 7. Cleanup test database
	if err := c.cleanupMongoDBTestDatabase(ctx, client, testDBName); err != nil {
		c.logger.WithError(err).Warnf("Failed to cleanup test database: %s", testDBName)
		// Do not return error as tests already passed
	}

	c.logger.Infof("MongoDB all permission tests passed: %s", connInfo.Endpoint)

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
		Password: password,
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

// testMongoDBDatabasePermissions tests database-level permissions
func (c *Collector) testMongoDBDatabasePermissions(
	ctx context.Context,
	client *mongo.Client,
	_ string,
) error {
	// Test LIST DATABASES
	if _, err := client.ListDatabaseNames(ctx, bson.M{}); err != nil {
		c.logger.WithError(err).Error("LIST DATABASES execution failed")
		return fmt.Errorf("failed to list databases: %w", err)
	}

	// Note: MongoDB creates databases implicitly when you insert data
	return nil
}

// testMongoDBCollectionPermissions tests collection-level permissions
func (c *Collector) testMongoDBCollectionPermissions(
	ctx context.Context,
	client *mongo.Client,
	testDBName string,
) error {
	testCollectionName := "test_collection"

	// Get test database and collection
	testDB := client.Database(testDBName)
	collection := testDB.Collection(testCollectionName)

	// Test INSERT (also implicitly creates database and collection)
	if err := c.testMongoDBInsert(ctx, collection); err != nil {
		return err
	}

	// Test SELECT (Find)
	if err := c.testMongoDBSelect(ctx, collection); err != nil {
		return err
	}

	// Test UPDATE
	if err := c.testMongoDBUpdate(ctx, collection); err != nil {
		return err
	}

	// Test DELETE
	if err := c.testMongoDBDelete(ctx, collection); err != nil {
		return err
	}

	// Test DROP COLLECTION
	if err := c.testMongoDBDropCollection(ctx, collection); err != nil {
		return err
	}

	return nil
}

// testMongoDBInsert tests INSERT permission
func (c *Collector) testMongoDBInsert(ctx context.Context, collection *mongo.Collection) error {
	document := bson.M{"id": 1, "name": "test"}
	if _, err := collection.InsertOne(ctx, document); err != nil {
		c.logger.WithError(err).Error("INSERT failed")
		return fmt.Errorf("failed to insert document: %w", err)
	}

	return nil
}

// testMongoDBSelect tests SELECT (Find) permission
func (c *Collector) testMongoDBSelect(ctx context.Context, collection *mongo.Collection) error {
	var result bson.M

	filter := bson.M{"id": 1}
	if err := collection.FindOne(ctx, filter).Decode(&result); err != nil {
		c.logger.WithError(err).Error("SELECT failed")
		return fmt.Errorf("failed to find document: %w", err)
	}

	// Verify result
	if result["name"] != "test" {
		c.logger.Errorf("SELECT result incorrect: %v", result)
		return fmt.Errorf("unexpected select result: %v", result)
	}

	return nil
}

// testMongoDBUpdate tests UPDATE permission
func (c *Collector) testMongoDBUpdate(ctx context.Context, collection *mongo.Collection) error {
	filter := bson.M{"id": 1}

	update := bson.M{"$set": bson.M{"name": "updated"}}
	if _, err := collection.UpdateOne(ctx, filter, update); err != nil {
		c.logger.WithError(err).Error("UPDATE failed")
		return fmt.Errorf("failed to update document: %w", err)
	}

	return nil
}

// testMongoDBDelete tests DELETE permission
func (c *Collector) testMongoDBDelete(ctx context.Context, collection *mongo.Collection) error {
	filter := bson.M{"id": 1}
	if _, err := collection.DeleteOne(ctx, filter); err != nil {
		c.logger.WithError(err).Error("DELETE failed")
		return fmt.Errorf("failed to delete document: %w", err)
	}

	return nil
}

// testMongoDBDropCollection tests DROP COLLECTION permission
func (c *Collector) testMongoDBDropCollection(
	ctx context.Context,
	collection *mongo.Collection,
) error {
	if err := collection.Drop(ctx); err != nil {
		c.logger.WithError(err).Error("DROP COLLECTION failed")
		return fmt.Errorf("failed to drop collection: %w", err)
	}

	return nil
}

// cleanupMongoDBTestDatabase cleans up the test database
func (c *Collector) cleanupMongoDBTestDatabase(
	ctx context.Context,
	client *mongo.Client,
	testDBName string,
) error {
	c.logger.Debugf("Cleaning up test database: %s", testDBName)

	if err := client.Database(testDBName).Drop(ctx); err != nil {
		c.logger.WithError(err).Warnf("Failed to drop test database: %s", testDBName)
		return fmt.Errorf("failed to drop test database: %w", err)
	}

	c.logger.Debugf("Test database cleaned up: %s", testDBName)

	return nil
}
