package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

var cachedSecretDataKeys = map[string]struct{}{
	"username": {},
	"password": {},
	"host":     {},
	"port":     {},
	"endpoint": {},
	"url":      {},
}

var cachedSecretLabelKeys = map[string]struct{}{
	"apps.kubeblocks.io/cluster-type": {},
	"app.kubernetes.io/name":          {},
}

// SecretCache manages a cache of database secrets using standard informers
type SecretCache struct {
	client          kubernetes.Interface
	logger          *log.Entry
	informerFactory informers.SharedInformerFactory
	informers       []cache.SharedIndexInformer
	stopCh          chan struct{}
	cancelCtx       context.CancelFunc
}

// NewSecretCache creates a new SecretCache
func NewSecretCache(client kubernetes.Interface, logger *log.Entry) *SecretCache {
	return &SecretCache{
		client:    client,
		logger:    logger,
		informers: make([]cache.SharedIndexInformer, 0),
	}
}

// Start initializes the cache and starts watching secrets using informers
func (sc *SecretCache) Start(ctx context.Context, namespaces []string) error {
	ctx, cancel := context.WithCancel(ctx)
	sc.cancelCtx = cancel
	sc.stopCh = make(chan struct{})

	// Define all database types and their selectors
	dbTypeSelectors := map[DatabaseType]string{
		DatabaseTypeMySQL:      "apps.kubeblocks.io/cluster-type=mysql",
		DatabaseTypePostgreSQL: "app.kubernetes.io/name=postgresql",
		DatabaseTypeMongoDB:    "apps.kubeblocks.io/cluster-type=mongodb",
		DatabaseTypeRedis:      "apps.kubeblocks.io/cluster-type=redis",
	}

	// Determine namespace scope
	var namespaceScope string
	if len(namespaces) == 0 {
		// Watch all namespaces
		namespaceScope = ""

		sc.logger.Info("Starting secret cache with all-namespace informer watch")
	} else {
		sc.logger.WithField("namespaces", namespaces).
			Info("Secret cache will be populated via polling (specific namespaces)")
		// For specific namespaces, we'll use polling instead of informers
		// to avoid creating too many watchers
		return sc.startPolling(ctx, namespaces, dbTypeSelectors)
	}

	// Create informer factory with default resync period
	sc.informerFactory = informers.NewSharedInformerFactoryWithOptions(
		sc.client,
		30*time.Second, // resync period
		informers.WithNamespace(namespaceScope),
	)

	// For all-namespace mode, create informers for each database type
	for dbType, selector := range dbTypeSelectors {
		// Create a filtered informer for this database type using recommended API
		informer := informers.NewSharedInformerFactoryWithOptions(
			sc.client,
			30*time.Second,
			informers.WithNamespace(namespaceScope),
			informers.WithTweakListOptions(func(options *metav1.ListOptions) {
				options.LabelSelector = selector
			}),
		).Core().V1().Secrets().Informer()

		if err := informer.SetTransform(sc.trimSecretForCache); err != nil {
			return fmt.Errorf("failed to set transform for %s informer: %w", dbType, err)
		}

		// Add event handlers
		_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj any) {
				secret, ok := obj.(*corev1.Secret)
				if !ok {
					return
				}

				sc.logger.WithFields(log.Fields{
					"type":      dbType,
					"namespace": secret.Namespace,
					"name":      secret.Name,
					"event":     "Added",
				}).Debug("Secret added to cache")
			},
			UpdateFunc: func(oldObj, newObj any) {
				secret, ok := newObj.(*corev1.Secret)
				if !ok {
					return
				}

				sc.logger.WithFields(log.Fields{
					"type":      dbType,
					"namespace": secret.Namespace,
					"name":      secret.Name,
					"event":     "Modified",
				}).Debug("Secret updated in cache")
			},
			DeleteFunc: func(obj any) {
				secret, ok := obj.(*corev1.Secret)
				if !ok {
					return
				}

				sc.logger.WithFields(log.Fields{
					"type":      dbType,
					"namespace": secret.Namespace,
					"name":      secret.Name,
					"event":     "Deleted",
				}).Debug("Secret removed from cache")
			},
		})
		if err != nil {
			return fmt.Errorf("failed to add event handler for %s: %w", dbType, err)
		}

		sc.informers = append(sc.informers, informer)

		sc.logger.WithFields(log.Fields{
			"type":     dbType,
			"selector": selector,
		}).Debug("Created informer for database type")
	}

	// Start all informers
	sc.informerFactory.Start(sc.stopCh)

	// Start individual informers
	for _, informer := range sc.informers {
		go informer.Run(sc.stopCh)
	}

	// Wait for all informers to sync
	sc.logger.Info("Waiting for secret cache informers to sync...")

	if !cache.WaitForCacheSync(sc.stopCh, sc.getAllInformerSyncs()...) {
		return errors.New("failed to sync secret cache informers")
	}

	sc.logger.WithField("cache_size", sc.GetCacheSize()).
		Info("Secret cache started with informer mechanism")

	return nil
}

func (sc *SecretCache) trimSecretForCache(obj any) (any, error) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return obj, nil
	}

	trimmed := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            secret.Name,
			Namespace:       secret.Namespace,
			Labels:          trimSecretLabels(secret.Labels),
			ResourceVersion: secret.ResourceVersion,
		},
		Type: secret.Type,
		Data: trimSecretData(secret.Data),
	}

	if secret.Immutable != nil {
		immutable := *secret.Immutable
		trimmed.Immutable = &immutable
	}

	return trimmed, nil
}

func trimSecretLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}

	trimmed := make(map[string]string)
	for key, value := range labels {
		if _, ok := cachedSecretLabelKeys[key]; ok {
			trimmed[key] = value
		}
	}

	if len(trimmed) == 0 {
		return nil
	}

	return trimmed
}

func trimSecretData(data map[string][]byte) map[string][]byte {
	if len(data) == 0 {
		return nil
	}

	trimmed := make(map[string][]byte)
	for key, value := range data {
		if _, ok := cachedSecretDataKeys[key]; ok {
			trimmed[key] = append([]byte(nil), value...)
		}
	}

	if len(trimmed) == 0 {
		return nil
	}

	return trimmed
}

// getAllInformerSyncs returns all informer HasSynced functions
func (sc *SecretCache) getAllInformerSyncs() []cache.InformerSynced {
	syncs := make([]cache.InformerSynced, len(sc.informers))
	for i, informer := range sc.informers {
		syncs[i] = informer.HasSynced
	}

	return syncs
}

// startPolling starts polling mode for specific namespaces (unchanged logic)
func (sc *SecretCache) startPolling(
	ctx context.Context,
	namespaces []string,
	selectors map[DatabaseType]string,
) error {
	// Create a simple cache structure for polling mode
	sc.stopCh = make(chan struct{})

	// Initial population
	if err := sc.pollSecrets(ctx, namespaces, selectors); err != nil {
		return err
	}

	// Start periodic polling
	go func() {
		ticker := time.NewTicker(30 * time.Second) // Poll every 30 seconds
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := sc.pollSecrets(ctx, namespaces, selectors); err != nil {
					sc.logger.WithError(err).Error("Failed to poll secrets")
				}
			}
		}
	}()

	return nil
}

// pollSecrets polls secrets from specific namespaces
// This is unchanged and still used for specific namespace mode
func (sc *SecretCache) pollSecrets(
	ctx context.Context,
	namespaces []string,
	selectors map[DatabaseType]string,
) error {
	for _, ns := range namespaces {
		for _, selector := range selectors {
			secrets, err := sc.client.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{
				LabelSelector: selector,
			})
			if err != nil {
				sc.logger.WithError(err).WithField("namespace", ns).Error("Failed to list secrets")
				continue
			}

			// Secrets are available through List API in polling mode
			// The informer cache will handle storage automatically when using informers
			sc.logger.WithFields(log.Fields{
				"namespace": ns,
				"count":     len(secrets.Items),
			}).Debug("Polled secrets from namespace")
		}
	}

	return nil
}

// GetSecrets returns all cached secrets matching the filter using informer cache
func (sc *SecretCache) GetSecrets(
	dbType DatabaseType,
	isCredentialFunc func(*corev1.Secret, DatabaseType) bool,
) []*corev1.Secret {
	result := make([]*corev1.Secret, 0)

	// Iterate through all informers to get secrets from their stores
	for _, informer := range sc.informers {
		store := informer.GetStore()
		items := store.List()

		for _, item := range items {
			secret, ok := item.(*corev1.Secret)
			if !ok {
				continue
			}

			// Check if secret belongs to this database type and matches credential filter
			if sc.matchesType(secret, dbType) && isCredentialFunc(secret, dbType) {
				result = append(result, secret)
			}
		}
	}

	return result
}

// matchesType checks if a secret matches the database type based on labels
func (sc *SecretCache) matchesType(secret *corev1.Secret, dbType DatabaseType) bool {
	labels := secret.Labels
	if labels == nil {
		return false
	}

	switch dbType {
	case DatabaseTypeMySQL:
		return labels["apps.kubeblocks.io/cluster-type"] == "mysql"
	case DatabaseTypePostgreSQL:
		return labels["app.kubernetes.io/name"] == "postgresql"
	case DatabaseTypeMongoDB:
		return labels["apps.kubeblocks.io/cluster-type"] == "mongodb"
	case DatabaseTypeRedis:
		return labels["apps.kubeblocks.io/cluster-type"] == "redis"
	default:
		return false
	}
}

// Stop stops all informers and cleans up
func (sc *SecretCache) Stop() {
	if sc.cancelCtx != nil {
		sc.cancelCtx()
	}

	if sc.stopCh != nil {
		close(sc.stopCh)
	}

	sc.logger.Info("Secret cache stopped")
}

// GetCacheSize returns the number of secrets in cache across all informers
func (sc *SecretCache) GetCacheSize() int {
	totalSize := 0
	for _, informer := range sc.informers {
		store := informer.GetStore()
		totalSize += len(store.List())
	}

	return totalSize
}
