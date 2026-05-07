// pkg/collector/crds/informer.go
package informer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// ResourceEventStore receives informer resource events.
type ResourceEventStore interface {
	Add(obj *unstructured.Unstructured)
	Update(obj *unstructured.Unstructured)
	Delete(obj *unstructured.Unstructured)
	Len() int
}

// Informer watches Kubernetes resources.
// Responsibilities: Monitors K8s API resource changes and syncs derived state.
type Informer struct {
	// Kubernetes related
	config        *InformerConfig
	dynamicClient dynamic.Interface
	informer      cache.SharedIndexInformer

	// Lifecycle management
	informerStopCh chan struct{}
	started        bool

	// Storage layer reference (core dependency)
	store ResourceEventStore

	// Logger instance
	logger *log.Entry
}

// InformerConfig configures the informer.
type InformerConfig struct {
	// GVR is the resource type to watch
	GVR schema.GroupVersionResource

	// Resync period
	ResyncPeriod time.Duration

	// Namespaces restricts the watched namespaces. Empty means all namespaces.
	Namespaces []string

	// TransformPaths defines fields to keep in the informer cache.
	TransformPaths []string
}

// NewInformer creates a new Informer.
// Parameters:
//   - dynamicClient: Kubernetes dynamic client
//   - config: Informer configuration
//   - resourceStore: Resource event storage layer (dependency injection)
//   - logger: Logger instance
func NewInformer(
	dynamicClient dynamic.Interface,
	config *InformerConfig,
	resourceStore ResourceEventStore,
	logger *log.Entry,
) (*Informer, error) {
	// Validate parameters
	if config == nil {
		return nil, errors.New("config cannot be nil")
	}

	if dynamicClient == nil {
		return nil, errors.New("dynamic client cannot be nil")
	}

	if resourceStore == nil {
		return nil, errors.New("resource store cannot be nil")
	}

	if config.ResyncPeriod == 0 {
		config.ResyncPeriod = 10 * time.Minute
	}

	if logger == nil {
		logger = log.WithField("component", "informer")
	}

	informerLogger := logger.WithFields(log.Fields{
		"gvr": config.GVR.String(),
	})

	return &Informer{
		config:        config,
		dynamicClient: dynamicClient,
		store:         resourceStore,
		logger:        informerLogger,
		started:       false,
	}, nil
}

// Start starts the Informer and begins watching resources.
func (i *Informer) Start(ctx context.Context) error {
	if i.started {
		return errors.New("informer already started")
	}

	i.logger.Info("Starting dynamic informer")

	namespace := i.factoryNamespace()
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		i.dynamicClient,
		i.config.ResyncPeriod,
		namespace,
		nil,
	)
	i.logger.WithField("namespace", namespace).Debug("Created dynamic informer factory")

	i.informer = factory.ForResource(i.config.GVR).Informer()
	if len(i.config.TransformPaths) > 0 {
		if err := i.informer.SetTransform(i.transformObject); err != nil {
			return fmt.Errorf("failed to set informer transform: %w", err)
		}
	}

	if err := i.registerEventHandlers(); err != nil {
		return fmt.Errorf("failed to register event handlers: %w", err)
	}

	i.informerStopCh = make(chan struct{})
	go i.informer.Run(i.informerStopCh)

	// Wait for cache sync
	i.logger.Info("Waiting for informer cache to sync")

	if !cache.WaitForCacheSync(ctx.Done(), i.informer.HasSynced) {
		close(i.informerStopCh)
		i.informerStopCh = nil
		return errors.New("failed to sync informer cache")
	}

	i.started = true
	i.logger.Info("Dynamic informer started and cache synced successfully")

	// Log initial statistics
	i.logInitialStats()

	return nil
}

// Stop stops the Informer.
func (i *Informer) Stop() error {
	if !i.started {
		i.logger.Warn("Informer not started, nothing to stop")
		return nil
	}

	i.logger.Info("Stopping dynamic informer")

	if i.informerStopCh != nil {
		close(i.informerStopCh)
		i.informerStopCh = nil
	}

	i.started = false
	i.logger.Info("Dynamic informer stopped")

	return nil
}

// HasSynced returns whether the Informer cache is synced.
func (i *Informer) HasSynced() bool {
	if i.informer == nil {
		return false
	}
	return i.informer.HasSynced()
}

// IsStarted returns whether the Informer has been started.
func (i *Informer) IsStarted() bool {
	return i.started
}

// GetStore returns the internal cache.Store (for debugging).
// Note: Direct use is not recommended; data should be accessed through the derived store.
func (i *Informer) GetStore() cache.Store {
	if i.informer == nil {
		return nil
	}
	return i.informer.GetStore()
}

// GetConfig returns the Informer configuration.
func (i *Informer) GetConfig() *InformerConfig {
	return i.config
}

// registerEventHandlers registers event handlers.
func (i *Informer) registerEventHandlers() error {
	_, err := i.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			u := i.extractUnstructured(obj)
			if u != nil {
				i.handleAdd(u)
			}
		},
		UpdateFunc: func(oldObj, newObj any) {
			oldU := i.extractUnstructured(oldObj)

			newU := i.extractUnstructured(newObj)
			if oldU != nil && newU != nil {
				i.handleUpdate(oldU, newU)
			}
		},
		DeleteFunc: func(obj any) {
			u := i.extractUnstructured(obj)
			if u != nil {
				i.handleDelete(u)
			}
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler: %w", err)
	}

	i.logger.Debug("Event handlers registered successfully")

	return nil
}

func (i *Informer) transformObject(obj any) (any, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return obj, nil
	}

	transformed := &unstructured.Unstructured{Object: make(map[string]any)}
	transformed.SetName(u.GetName())
	transformed.SetNamespace(u.GetNamespace())
	transformed.SetUID(u.GetUID())
	transformed.SetResourceVersion(u.GetResourceVersion())
	transformed.SetGeneration(u.GetGeneration())

	for _, path := range i.config.TransformPaths {
		copyNestedField(u.Object, transformed.Object, path)
	}

	return transformed, nil
}

func copyNestedField(src, dst map[string]any, path string) {
	if path == "" {
		return
	}

	parts := strings.Split(path, ".")

	value, found, err := unstructured.NestedFieldCopy(src, parts...)
	if err != nil || !found {
		return
	}

	_ = unstructured.SetNestedField(dst, value, parts...)
}

func (i *Informer) factoryNamespace() string {
	namespaces := make([]string, 0, len(i.config.Namespaces))
	for _, namespace := range i.config.Namespaces {
		if namespace == "" {
			continue
		}

		namespaces = append(namespaces, namespace)
	}

	if len(namespaces) == 1 {
		return namespaces[0]
	}

	return ""
}

// handleAdd handles resource add events.
func (i *Informer) handleAdd(obj *unstructured.Unstructured) {
	i.logger.WithFields(log.Fields{
		"namespace": obj.GetNamespace(),
		"name":      obj.GetName(),
		"uid":       obj.GetUID(),
	}).Debug("Resource added")

	// Write directly to the derived store.
	i.store.Add(obj)
}

// handleUpdate handles resource update events.
func (i *Informer) handleUpdate(oldObj, newObj *unstructured.Unstructured) {
	// Check if ResourceVersion changed (avoid invalid updates)
	if oldObj.GetResourceVersion() == newObj.GetResourceVersion() {
		i.logger.WithFields(log.Fields{
			"namespace": newObj.GetNamespace(),
			"name":      newObj.GetName(),
		}).Debug("Resource update ignored (same resource version)")

		return
	}

	i.logger.WithFields(log.Fields{
		"namespace":   newObj.GetNamespace(),
		"name":        newObj.GetName(),
		"uid":         newObj.GetUID(),
		"old_version": oldObj.GetResourceVersion(),
		"new_version": newObj.GetResourceVersion(),
	}).Debug("Resource updated")

	// Write directly to the derived store.
	i.store.Update(newObj)
}

// handleDelete handles resource delete events.
func (i *Informer) handleDelete(obj *unstructured.Unstructured) {
	i.logger.WithFields(log.Fields{
		"namespace": obj.GetNamespace(),
		"name":      obj.GetName(),
		"uid":       obj.GetUID(),
	}).Debug("Resource deleted")

	// Delete from the derived store.
	i.store.Delete(obj)
}

// extractUnstructured extracts Unstructured from event object.
// Handles the special case of DeletedFinalStateUnknown.
func (i *Informer) extractUnstructured(obj any) *unstructured.Unstructured {
	// Direct type assertion
	if u, ok := obj.(*unstructured.Unstructured); ok {
		return u
	}

	// Handle DeletedFinalStateUnknown (special case for delete events)
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		if u, ok := tombstone.Obj.(*unstructured.Unstructured); ok {
			i.logger.WithFields(log.Fields{
				"namespace": u.GetNamespace(),
				"name":      u.GetName(),
			}).Debug("Extracted object from tombstone")

			return u
		}

		i.logger.WithField("object", tombstone.Obj).
			Error("Tombstone contained object that is not Unstructured")

		return nil
	}

	// Unrecognized type
	i.logger.WithField("object_type", fmt.Sprintf("%T", obj)).
		Error("Failed to extract Unstructured from object")

	return nil
}

// logInitialStats logs initial statistics.
func (i *Informer) logInitialStats() {
	storeLen := i.store.Len()

	cacheLen := 0
	if i.informer != nil && i.informer.GetStore() != nil {
		cacheLen = len(i.informer.GetStore().List())
	}

	i.logger.WithFields(log.Fields{
		"store_count": storeLen,
		"cache_count": cacheLen,
	}).Info("Initial sync completed")
}

// GetResourceCount returns the number of currently watched resources.
func (i *Informer) GetResourceCount() int {
	return i.store.Len()
}

// LogStats logs current statistics (for debugging and monitoring).
func (i *Informer) LogStats() {
	if !i.started {
		i.logger.Warn("Informer not started")
		return
	}

	i.logger.WithFields(log.Fields{
		"synced":      i.HasSynced(),
		"store_count": i.store.Len(),
	}).Info("Informer statistics")
}

// WaitForSync waits for Informer cache sync (with timeout).
func (i *Informer) WaitForSync(timeout time.Duration) error {
	if !i.started {
		return errors.New("informer not started")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if !cache.WaitForCacheSync(ctx.Done(), i.informer.HasSynced) {
		return fmt.Errorf("failed to sync cache within %v", timeout)
	}

	return nil
}

func (i *Informer) Resync() error {
	if !i.started {
		return errors.New("informer not started")
	}

	if i.informer == nil {
		return errors.New("informer not initialized")
	}

	i.logger.Info("Triggering manual resync")

	return nil
}
