// Package auth provides Kubernetes authentication middleware for HTTP endpoints
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// Authenticator handles Kubernetes authentication and authorization
type Authenticator struct {
	client       kubernetes.Interface
	authCache    *authCache
	authzCache   *authzCache
	retryBackoff wait.Backoff
}

// authCache caches TokenReview results
type authCache struct {
	mu    sync.RWMutex
	cache map[string]*authCacheEntry
}

type authCacheEntry struct {
	userInfo  *authenticationv1.UserInfo
	expiresAt time.Time
}

// authzCache caches SubjectAccessReview results
type authzCache struct {
	mu    sync.RWMutex
	cache map[string]*authzCacheEntry
}

type authzCacheEntry struct {
	allowed   bool
	expiresAt time.Time
}

// NewAuthenticator creates a new authenticator with caching
func NewAuthenticator(client kubernetes.Interface) *Authenticator {
	a := &Authenticator{
		client: client,
		authCache: &authCache{
			cache: make(map[string]*authCacheEntry),
		},
		authzCache: &authzCache{
			cache: make(map[string]*authzCacheEntry),
		},
		// Retry backoff configuration from controller-runtime
		retryBackoff: wait.Backoff{
			Duration: 500 * time.Millisecond,
			Factor:   1.5,
			Jitter:   0.2,
			Steps:    5,
		},
	}

	// Start cache cleanup goroutine
	go a.cleanupExpiredEntries()

	return a
}

// cleanupExpiredEntries periodically removes expired cache entries
func (a *Authenticator) cleanupExpiredEntries() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()

		// Clean authentication cache
		a.authCache.mu.Lock()

		for key, entry := range a.authCache.cache {
			if now.After(entry.expiresAt) {
				delete(a.authCache.cache, key)
			}
		}

		a.authCache.mu.Unlock()

		// Clean authorization cache
		a.authzCache.mu.Lock()

		for key, entry := range a.authzCache.cache {
			if now.After(entry.expiresAt) {
				delete(a.authzCache.cache, key)
			}
		}

		a.authzCache.mu.Unlock()
	}
}

// Middleware returns an HTTP middleware that authenticates requests using Kubernetes TokenReview
// and authorizes them using SubjectAccessReview
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract bearer token from Authorization header
		token := extractBearerToken(r)
		if token == "" {
			log.Debug("No bearer token found in request")
			http.Error(w, "Unauthorized: no bearer token provided", http.StatusUnauthorized)
			return
		}

		// Authenticate the token using TokenReview (with caching)
		userInfo, err := a.authenticateTokenCached(r.Context(), token)
		if err != nil {
			log.WithError(err).Warn("Token authentication failed")
			http.Error(w, fmt.Sprintf("Unauthorized: %v", err), http.StatusUnauthorized)
			return
		}

		// Authorize the request using SubjectAccessReview (with caching)
		allowed, err := a.authorizeRequestCached(r.Context(), userInfo, r.URL.Path, r.Method)
		if err != nil {
			log.WithError(err).Error("Authorization check failed")
			http.Error(w, fmt.Sprintf("Internal error: %v", err), http.StatusInternalServerError)

			return
		}

		if !allowed {
			log.WithFields(log.Fields{
				"user":   userInfo.Username,
				"path":   r.URL.Path,
				"method": r.Method,
			}).Warn("Authorization denied")
			http.Error(w, "Forbidden: insufficient permissions", http.StatusForbidden)

			return
		}

		log.WithFields(log.Fields{
			"user": userInfo.Username,
			"path": r.URL.Path,
		}).Debug("Request authenticated and authorized")

		// Continue to the next handler
		next.ServeHTTP(w, r)
	})
}

// extractBearerToken extracts the bearer token from the Authorization header
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}

	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(auth, bearerPrefix) {
		return ""
	}

	return strings.TrimPrefix(auth, bearerPrefix)
}

// authenticateTokenCached validates the token with caching
func (a *Authenticator) authenticateTokenCached(
	ctx context.Context,
	token string,
) (*authenticationv1.UserInfo, error) {
	// Generate cache key (hash of token for security)
	cacheKey := hashToken(token)

	// Check cache first
	a.authCache.mu.RLock()

	if entry, ok := a.authCache.cache[cacheKey]; ok {
		if time.Now().Before(entry.expiresAt) {
			a.authCache.mu.RUnlock()
			log.Debug("Authentication cache hit")
			return entry.userInfo, nil
		}
	}

	a.authCache.mu.RUnlock()

	// Cache miss, perform authentication
	userInfo, err := a.authenticateToken(ctx, token)
	if err != nil {
		return nil, err
	}

	// Cache the result (TTL: 1 minute, same as controller-runtime)
	a.authCache.mu.Lock()
	a.authCache.cache[cacheKey] = &authCacheEntry{
		userInfo:  userInfo,
		expiresAt: time.Now().Add(1 * time.Minute),
	}
	a.authCache.mu.Unlock()

	return userInfo, nil
}

// authenticateToken validates the token using Kubernetes TokenReview API with retry
func (a *Authenticator) authenticateToken(
	ctx context.Context,
	token string,
) (*authenticationv1.UserInfo, error) {
	var (
		result  *authenticationv1.TokenReview
		lastErr error
	)

	// Retry with exponential backoff
	err := wait.ExponentialBackoffWithContext(
		ctx,
		a.retryBackoff,
		func(ctx context.Context) (bool, error) {
			tokenReview := &authenticationv1.TokenReview{
				Spec: authenticationv1.TokenReviewSpec{
					Token: token,
				},
			}

			var err error

			result, err = a.client.AuthenticationV1().TokenReviews().Create(
				ctx,
				tokenReview,
				metav1.CreateOptions{},
			)
			if err != nil {
				lastErr = err
				log.WithError(err).Debug("TokenReview API call failed, retrying...")
				return false, nil // Retry
			}

			return true, nil // Success
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to call TokenReview API after retries: %w", lastErr)
	}

	// Check if authentication succeeded
	if !result.Status.Authenticated {
		return nil, fmt.Errorf("token authentication failed: %s", result.Status.Error)
	}

	return &result.Status.User, nil
}

// hashToken creates a hash of the token for cache key
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// authorizeRequestCached checks authorization with caching
func (a *Authenticator) authorizeRequestCached(
	ctx context.Context,
	userInfo *authenticationv1.UserInfo,
	path string,
	method string,
) (bool, error) {
	// Generate cache key
	verb := strings.ToLower(method)
	cacheKey := fmt.Sprintf("%s:%s:%s", userInfo.Username, path, verb)

	// Check cache first
	a.authzCache.mu.RLock()

	if entry, ok := a.authzCache.cache[cacheKey]; ok {
		if time.Now().Before(entry.expiresAt) {
			allowed := entry.allowed

			a.authzCache.mu.RUnlock()
			log.WithField("allowed", allowed).Debug("Authorization cache hit")
			return allowed, nil
		}
	}

	a.authzCache.mu.RUnlock()

	// Cache miss, perform authorization
	allowed, err := a.authorizeRequest(ctx, userInfo, path, verb)
	if err != nil {
		return false, err
	}

	// Cache the result
	// Allow: 5 minutes TTL, Deny: 30 seconds TTL (same as controller-runtime)
	ttl := 30 * time.Second
	if allowed {
		ttl = 5 * time.Minute
	}

	a.authzCache.mu.Lock()
	a.authzCache.cache[cacheKey] = &authzCacheEntry{
		allowed:   allowed,
		expiresAt: time.Now().Add(ttl),
	}
	a.authzCache.mu.Unlock()

	return allowed, nil
}

// authorizeRequest checks if the user has permission to access the resource with retry
func (a *Authenticator) authorizeRequest(
	ctx context.Context,
	userInfo *authenticationv1.UserInfo,
	path string,
	verb string,
) (bool, error) {
	var (
		result  *authorizationv1.SubjectAccessReview
		lastErr error
	)

	// Retry with exponential backoff
	err := wait.ExponentialBackoffWithContext(
		ctx,
		a.retryBackoff,
		func(ctx context.Context) (bool, error) {
			sar := &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User:   userInfo.Username,
					Groups: userInfo.Groups,
					UID:    userInfo.UID,
					// For non-resource URLs (like /metrics)
					NonResourceAttributes: &authorizationv1.NonResourceAttributes{
						Path: path,
						Verb: verb,
					},
				},
			}

			var err error

			result, err = a.client.AuthorizationV1().SubjectAccessReviews().Create(
				ctx,
				sar,
				metav1.CreateOptions{},
			)
			if err != nil {
				lastErr = err
				log.WithError(err).Debug("SubjectAccessReview API call failed, retrying...")
				return false, nil // Retry
			}

			return true, nil // Success
		},
	)
	if err != nil {
		return false, fmt.Errorf(
			"failed to call SubjectAccessReview API after retries: %w",
			lastErr,
		)
	}

	return result.Status.Allowed, nil
}
