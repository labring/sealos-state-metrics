package database

import (
	"context"
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

// SecretCache manages a cache of database secrets with watch support
type SecretCache struct {
	client    kubernetes.Interface
	logger    *log.Entry
	mu        sync.RWMutex
	secrets   map[string]*corev1.Secret // key: namespace/name
	watchers  []watch.Interface
	cancelCtx context.CancelFunc
	// Track resource versions for each label selector
	resourceVersions map[string]string
}

// NewSecretCache creates a new SecretCache
func NewSecretCache(client kubernetes.Interface, logger *log.Entry) *SecretCache {
	return &SecretCache{
		client:           client,
		logger:           logger,
		secrets:          make(map[string]*corev1.Secret),
		watchers:         make([]watch.Interface, 0),
		resourceVersions: make(map[string]string),
	}
}

// Start initializes the cache and starts watching secrets
func (sc *SecretCache) Start(ctx context.Context, namespaces []string) error {
	ctx, cancel := context.WithCancel(ctx)
	sc.cancelCtx = cancel

	// Define all database types and their selectors
	dbTypeSelectors := map[DatabaseType]string{
		DatabaseTypeMySQL:      "apps.kubeblocks.io/cluster-type=mysql",
		DatabaseTypePostgreSQL: "app.kubernetes.io/name=postgresql",
		DatabaseTypeMongoDB:    "apps.kubeblocks.io/component-name=mongodb",
		DatabaseTypeRedis:      "apps.kubeblocks.io/component-name=redis",
	}

	// Determine namespace scope
	var namespaceScope string
	if len(namespaces) == 0 {
		// Watch all namespaces
		namespaceScope = ""
		sc.logger.Info("Starting secret cache with all-namespace watch")
	} else {
		sc.logger.WithField("namespaces", namespaces).Info("Secret cache will be populated via polling (specific namespaces)")
		// For specific namespaces, we'll use polling instead of watch
		// to avoid creating too many watchers
		return sc.startPolling(ctx, namespaces, dbTypeSelectors)
	}

	// For all-namespace mode, use watch
	for dbType, selector := range dbTypeSelectors {
		// Initial list to populate cache
		secrets, err := sc.client.CoreV1().Secrets(namespaceScope).List(ctx, metav1.ListOptions{
			LabelSelector: selector,
		})
		if err != nil {
			return fmt.Errorf("failed to list secrets for %s: %w", dbType, err)
		}

		// Populate cache
		for i := range secrets.Items {
			secret := &secrets.Items[i]
			key := secret.Namespace + "/" + secret.Name
			sc.mu.Lock()
			sc.secrets[key] = secret
			sc.mu.Unlock()
		}

		// Store resource version for this selector
		sc.resourceVersions[string(dbType)] = secrets.ResourceVersion

		sc.logger.WithFields(log.Fields{
			"type":  dbType,
			"count": len(secrets.Items),
		}).Debug("Populated secret cache for database type")

		// Start watch
		watcher, err := sc.client.CoreV1().Secrets(namespaceScope).Watch(ctx, metav1.ListOptions{
			LabelSelector:   selector,
			ResourceVersion: secrets.ResourceVersion,
			Watch:           true,
		})
		if err != nil {
			return fmt.Errorf("failed to start watch for %s: %w", dbType, err)
		}

		sc.watchers = append(sc.watchers, watcher)

		// Start watching in background
		go sc.watchSecrets(ctx, watcher, dbType)
	}

	sc.logger.Info("Secret cache started with watch mechanism")
	return nil
}

// startPolling starts polling mode for specific namespaces
func (sc *SecretCache) startPolling(ctx context.Context, namespaces []string, selectors map[DatabaseType]string) error {
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
func (sc *SecretCache) pollSecrets(ctx context.Context, namespaces []string, selectors map[DatabaseType]string) error {
	for _, ns := range namespaces {
		for dbType, selector := range selectors {
			secrets, err := sc.client.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{
				LabelSelector: selector,
			})
			if err != nil {
				sc.logger.WithError(err).WithFields(log.Fields{
					"namespace": ns,
					"type":      dbType,
				}).Error("Failed to list secrets")
				continue
			}

			// Update cache
			sc.mu.Lock()
			for i := range secrets.Items {
				secret := &secrets.Items[i]
				key := secret.Namespace + "/" + secret.Name
				sc.secrets[key] = secret
			}
			sc.mu.Unlock()
		}
	}

	return nil
}

// watchSecrets handles watch events for secrets
func (sc *SecretCache) watchSecrets(ctx context.Context, watcher watch.Interface, dbType DatabaseType) {
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			sc.logger.WithField("type", dbType).Debug("Stopping secret watch")
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				// Watch closed, need to restart
				sc.logger.WithField("type", dbType).Warn("Secret watch closed, will restart on next poll")
				return
			}

			secret, ok := event.Object.(*corev1.Secret)
			if !ok {
				continue
			}

			key := secret.Namespace + "/" + secret.Name

			switch event.Type {
			case watch.Added, watch.Modified:
				sc.mu.Lock()
				sc.secrets[key] = secret
				sc.mu.Unlock()
				sc.logger.WithFields(log.Fields{
					"type":      dbType,
					"namespace": secret.Namespace,
					"name":      secret.Name,
					"event":     event.Type,
				}).Debug("Secret cache updated")

			case watch.Deleted:
				sc.mu.Lock()
				delete(sc.secrets, key)
				sc.mu.Unlock()
				sc.logger.WithFields(log.Fields{
					"type":      dbType,
					"namespace": secret.Namespace,
					"name":      secret.Name,
				}).Debug("Secret removed from cache")
			}
		}
	}
}

// GetSecrets returns all cached secrets matching the filter
func (sc *SecretCache) GetSecrets(dbType DatabaseType, isCredentialFunc func(*corev1.Secret, DatabaseType) bool) []*corev1.Secret {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	result := make([]*corev1.Secret, 0)
	for _, secret := range sc.secrets {
		// Check if secret belongs to this database type by checking its labels
		if sc.matchesType(secret, dbType) && isCredentialFunc(secret, dbType) {
			result = append(result, secret)
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
		return labels["apps.kubeblocks.io/component-name"] == "mongodb"
	case DatabaseTypeRedis:
		return labels["apps.kubeblocks.io/component-name"] == "redis"
	default:
		return false
	}
}

// Stop stops all watchers and cleans up
func (sc *SecretCache) Stop() {
	if sc.cancelCtx != nil {
		sc.cancelCtx()
	}

	for _, watcher := range sc.watchers {
		watcher.Stop()
	}

	sc.mu.Lock()
	sc.secrets = make(map[string]*corev1.Secret)
	sc.mu.Unlock()

	sc.logger.Info("Secret cache stopped")
}

// GetCacheSize returns the number of secrets in cache
func (sc *SecretCache) GetCacheSize() int {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return len(sc.secrets)
}
