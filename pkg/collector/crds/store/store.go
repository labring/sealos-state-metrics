// pkg/collector/crds/store/resource_store.go
package store

import (
	"fmt"
	"sync"
	"sync/atomic"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ResourceStore is the resource storage layer.
// Responsibilities: Manages in-memory cache of all CRD resources, providing thread-safe CRUD operations.
type ResourceStore struct {
	// Resource storage: key format is "namespace/name" or "name" (for cluster-scoped)
	mu        sync.RWMutex
	resources map[string]*unstructured.Unstructured

	// Logger instance
	logger *log.Entry

	// Configuration
	config *StoreConfig

	// Statistics
	metrics StoreMetrics
}

// StoreConfig configures the store.
type StoreConfig struct {
	// Whether to enable deep copy (prevents external modifications)
	// Recommended to enable, though it has performance overhead, it ensures data safety
	EnableDeepCopy bool

	// Resource type identifier (for logging and debugging)
	// Example: "metering.sealos.io/v1/Account"
	ResourceType string

	// Maximum cache size (0 means unlimited)
	// Warning log will be recorded when exceeded
	MaxSize int
}

// StoreMetrics tracks storage layer statistics.
type StoreMetrics struct {
	AddCount    atomic.Int64 // Add operation count
	UpdateCount atomic.Int64 // Update operation count
	DeleteCount atomic.Int64 // Delete operation count
	TotalCount  atomic.Int64 // Current total count
}

// NewResourceStore creates a new resource storage.
func NewResourceStore(logger *log.Entry, config *StoreConfig) *ResourceStore {
	if config == nil {
		config = &StoreConfig{
			EnableDeepCopy: true, // Deep copy enabled by default
			ResourceType:   "unknown",
			MaxSize:        0, // Unlimited
		}
	}

	if logger == nil {
		logger = log.WithField("component", "resource_store")
	}

	store := &ResourceStore{
		resources: make(map[string]*unstructured.Unstructured),
		logger:    logger.WithField("resource_type", config.ResourceType),
		config:    config,
	}

	store.logger.WithFields(log.Fields{
		"deep_copy": config.EnableDeepCopy,
		"max_size":  config.MaxSize,
	}).Info("Resource store initialized")

	return store
}

// Add adds a resource to the store.
// If the resource already exists, it will be overwritten (equivalent to Update).
func (s *ResourceStore) Add(obj *unstructured.Unstructured) {
	if obj == nil {
		s.logger.Warn("Attempted to add nil object")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.getKey(obj)

	// Check if already exists
	_, exists := s.resources[key]

	// Store object (optional deep copy)
	if s.config.EnableDeepCopy {
		s.resources[key] = obj.DeepCopy()
	} else {
		s.resources[key] = obj
	}

	// Update statistics
	if exists {
		s.metrics.UpdateCount.Add(1)
		s.logger.WithField("key", key).Debug("Resource updated (via Add)")
	} else {
		s.metrics.AddCount.Add(1)
		s.metrics.TotalCount.Add(1)
		s.logger.WithField("key", key).Debug("Resource added")
	}

	// Check size limit
	s.checkSizeLimit()
}

// Update updates a resource in the store.
// If the resource doesn't exist, it will be added (equivalent to Add).
func (s *ResourceStore) Update(obj *unstructured.Unstructured) {
	if obj == nil {
		s.logger.Warn("Attempted to update nil object")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.getKey(obj)

	// Check if already exists
	_, exists := s.resources[key]

	// Store object (optional deep copy)
	if s.config.EnableDeepCopy {
		s.resources[key] = obj.DeepCopy()
	} else {
		s.resources[key] = obj
	}

	// Update statistics
	if exists {
		s.metrics.UpdateCount.Add(1)
		s.logger.WithField("key", key).Debug("Resource updated")
	} else {
		s.metrics.AddCount.Add(1)
		s.metrics.TotalCount.Add(1)
		s.logger.WithField("key", key).Debug("Resource added (via Update)")
	}

	// Check size limit
	s.checkSizeLimit()
}

// Delete removes a resource from the store.
func (s *ResourceStore) Delete(obj *unstructured.Unstructured) {
	if obj == nil {
		s.logger.Warn("Attempted to delete nil object")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.getKey(obj)

	// Check if exists
	if _, exists := s.resources[key]; exists {
		delete(s.resources, key)
		s.metrics.DeleteCount.Add(1)
		s.metrics.TotalCount.Add(-1)
		s.logger.WithField("key", key).Debug("Resource deleted")
	} else {
		s.logger.WithField("key", key).Debug("Resource not found for deletion")
	}
}

// Get retrieves a resource by namespace and name.
// Returns the resource object and a flag indicating whether it exists.
func (s *ResourceStore) Get(namespace, name string) (*unstructured.Unstructured, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := s.makeKey(namespace, name)
	obj, exists := s.resources[key]

	if !exists {
		return nil, false
	}

	// Return deep copy to prevent external modifications
	if s.config.EnableDeepCopy {
		return obj.DeepCopy(), true
	}

	return obj, true
}

// List returns a list of all resources.
// Returns copies of resources (if deep copy is enabled).
func (s *ResourceStore) List() []*unstructured.Unstructured {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*unstructured.Unstructured, 0, len(s.resources))

	for _, obj := range s.resources {
		if s.config.EnableDeepCopy {
			result = append(result, obj.DeepCopy())
		} else {
			result = append(result, obj)
		}
	}

	return result
}

// ListByNamespace returns all resources in the specified namespace.
// If namespace is an empty string, returns all cluster-scoped resources.
func (s *ResourceStore) ListByNamespace(namespace string) []*unstructured.Unstructured {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*unstructured.Unstructured, 0)

	for _, obj := range s.resources {
		if obj.GetNamespace() == namespace {
			if s.config.EnableDeepCopy {
				result = append(result, obj.DeepCopy())
			} else {
				result = append(result, obj)
			}
		}
	}

	return result
}

// Len returns the number of currently stored resources.
func (s *ResourceStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.resources)
}

// Clear removes all resources.
// Note: This operation is irreversible, use with caution.
func (s *ResourceStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := len(s.resources)
	s.resources = make(map[string]*unstructured.Unstructured)
	s.metrics.TotalCount.Store(0)

	s.logger.WithField("cleared_count", count).Info("Resource store cleared")
}

// GetMetrics returns a snapshot of storage statistics.
func (s *ResourceStore) GetMetrics() StoreMetricsSnapshot {
	return StoreMetricsSnapshot{
		AddCount:    s.metrics.AddCount.Load(),
		UpdateCount: s.metrics.UpdateCount.Load(),
		DeleteCount: s.metrics.DeleteCount.Load(),
		TotalCount:  s.metrics.TotalCount.Load(),
	}
}

// StoreMetricsSnapshot is a snapshot of statistics (for return).
type StoreMetricsSnapshot struct {
	AddCount    int64
	UpdateCount int64
	DeleteCount int64
	TotalCount  int64
}

// GetResourceType returns the resource type identifier.
func (s *ResourceStore) GetResourceType() string {
	return s.config.ResourceType
}

// IsEmpty checks if the store is empty.
func (s *ResourceStore) IsEmpty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.resources) == 0
}

// Contains checks if the specified resource exists.
func (s *ResourceStore) Contains(namespace, name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := s.makeKey(namespace, name)
	_, exists := s.resources[key]
	return exists
}

// getKey generates a storage key from an Unstructured object.
// Internal method, must be called while holding the lock.
func (s *ResourceStore) getKey(obj *unstructured.Unstructured) string {
	namespace := obj.GetNamespace()
	name := obj.GetName()
	return s.makeKey(namespace, name)
}

// makeKey generates a storage key from namespace and name.
// Format:
//   - namespace-scoped: "namespace/name"
//   - cluster-scoped: "name"
func (s *ResourceStore) makeKey(namespace, name string) string {
	if namespace == "" {
		// Cluster-scoped resource
		return name
	}
	// Namespace-scoped resource
	return fmt.Sprintf("%s/%s", namespace, name)
}

// checkSizeLimit checks if storage size exceeds the limit.
// Internal method, must be called while holding the lock.
func (s *ResourceStore) checkSizeLimit() {
	if s.config.MaxSize > 0 && len(s.resources) > s.config.MaxSize {
		s.logger.WithFields(log.Fields{
			"current_size": len(s.resources),
			"max_size":     s.config.MaxSize,
		}).Warn("Resource store size exceeded limit")
	}
}

// LogStats logs current storage statistics (for debugging).
func (s *ResourceStore) LogStats() {
	metrics := s.GetMetrics()
	s.logger.WithFields(log.Fields{
		"total_count":  metrics.TotalCount,
		"add_count":    metrics.AddCount,
		"update_count": metrics.UpdateCount,
		"delete_count": metrics.DeleteCount,
	}).Info("Resource store statistics")
}

// GetKeys returns all stored keys (for debugging).
func (s *ResourceStore) GetKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.resources))
	for key := range s.resources {
		keys = append(keys, key)
	}
	return keys
}
