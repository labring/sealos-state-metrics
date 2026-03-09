package database

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// PreflightError represents a fast-fail error type
type PreflightError struct {
	Type    string // "no_service", "no_endpoints", "dns_failed", "invalid_host"
	Message string
}

func (e *PreflightError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Type, e.Message)
}

// PreflightChecker performs fast-fail checks before attempting database connections
type PreflightChecker struct {
	client kubernetes.Interface
	logger *log.Entry
}

// NewPreflightChecker creates a new PreflightChecker
func NewPreflightChecker(client kubernetes.Interface, logger *log.Entry) *PreflightChecker {
	return &PreflightChecker{
		client: client,
		logger: logger,
	}
}

// CheckDatabase performs preflight checks on a database connection
// Returns nil if all checks pass, otherwise returns a PreflightError
func (pc *PreflightChecker) CheckDatabase(
	ctx context.Context,
	namespace, dbName string,
	dbType DatabaseType,
	secret *corev1.Secret,
) *PreflightError {
	// Extract connection info
	host, err := pc.extractHost(secret, dbType)
	if err != nil {
		return &PreflightError{
			Type:    "invalid_host",
			Message: fmt.Sprintf("failed to extract host: %v", err),
		}
	}

	// If host is not a service hostname, do basic checks
	if !pc.isServiceHost(host, namespace) {
		// For non-service hosts (direct IP, external domain), do DNS check
		if err := pc.checkDNS(host); err != nil {
			return &PreflightError{
				Type:    "dns_failed",
				Message: err.Error(),
			}
		}

		return nil
	}

	// For service hosts, do comprehensive checks
	serviceName := pc.extractServiceName(host)

	// Check if service exists
	if err := pc.checkServiceExists(ctx, namespace, serviceName); err != nil {
		return &PreflightError{
			Type:    "no_service",
			Message: err.Error(),
		}
	}

	// Check if service has endpoints
	if err := pc.checkServiceEndpoints(ctx, namespace, serviceName); err != nil {
		return &PreflightError{
			Type:    "no_endpoints",
			Message: err.Error(),
		}
	}

	return nil
}

// extractHost extracts the hostname from the secret based on database type
func (pc *PreflightChecker) extractHost(
	secret *corev1.Secret,
	dbType DatabaseType,
) (string, error) {
	data := secret.Data

	switch dbType {
	case DatabaseTypeMySQL, DatabaseTypePostgreSQL:
		// These typically have 'host' field
		if host, ok := data["host"]; ok {
			return string(host), nil
		}
		// Fallback to parsing from connection string
		if connStr, ok := data["url"]; ok {
			return pc.parseHostFromURL(string(connStr))
		}

		return "", errors.New("no host or url found in secret")

	case DatabaseTypeMongoDB:
		// MongoDB uses connection URI
		if uri, ok := data["url"]; ok {
			return pc.parseHostFromURL(string(uri))
		}

		if host, ok := data["host"]; ok {
			return string(host), nil
		}

		return "", errors.New("no url or host found in secret")

	case DatabaseTypeRedis:
		// Redis typically has host field
		if host, ok := data["host"]; ok {
			return string(host), nil
		}

		if connStr, ok := data["url"]; ok {
			return pc.parseHostFromURL(string(connStr))
		}

		return "", errors.New("no host or url found in secret")

	default:
		return "", fmt.Errorf("unsupported database type: %s", dbType)
	}
}

// parseHostFromURL extracts hostname from a connection URL/URI
func (pc *PreflightChecker) parseHostFromURL(urlStr string) (string, error) {
	// Remove protocol prefix
	urlStr = strings.TrimPrefix(urlStr, "mysql://")
	urlStr = strings.TrimPrefix(urlStr, "postgresql://")
	urlStr = strings.TrimPrefix(urlStr, "postgres://")
	urlStr = strings.TrimPrefix(urlStr, "mongodb://")
	urlStr = strings.TrimPrefix(urlStr, "redis://")

	// Remove credentials
	if idx := strings.Index(urlStr, "@"); idx != -1 {
		urlStr = urlStr[idx+1:]
	}

	// Extract host:port
	if idx := strings.Index(urlStr, "/"); idx != -1 {
		urlStr = urlStr[:idx]
	}

	if idx := strings.Index(urlStr, "?"); idx != -1 {
		urlStr = urlStr[:idx]
	}

	// Remove port if present
	host, _, err := net.SplitHostPort(urlStr)
	if err != nil {
		// No port, use as-is
		host = urlStr
	}

	if host == "" {
		return "", errors.New("failed to parse host from URL")
	}

	return host, nil
}

// isServiceHost checks if the host appears to be a Kubernetes service
func (pc *PreflightChecker) isServiceHost(host, namespace string) bool {
	// Check for typical service DNS patterns:
	// - servicename.namespace.svc.cluster.local
	// - servicename.namespace.svc
	// - servicename.namespace
	// - servicename (if in same namespace)
	return strings.Contains(host, ".svc.cluster.local") ||
		strings.Contains(host, ".svc") ||
		(strings.Contains(host, ".") && strings.Contains(host, namespace))
}

// extractServiceName extracts service name from service hostname
func (pc *PreflightChecker) extractServiceName(host string) string {
	// Remove .namespace.svc.cluster.local suffix
	host = strings.Split(host, ".")[0]
	return host
}

// checkDNS performs a quick DNS lookup
func (pc *PreflightChecker) checkDNS(host string) error {
	// Quick DNS resolution check
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resolver := &net.Resolver{}

	_, err := resolver.LookupHost(ctx, host)
	if err != nil {
		return fmt.Errorf("DNS lookup failed for %s: %w", host, err)
	}

	return nil
}

// checkServiceExists checks if a service exists
func (pc *PreflightChecker) checkServiceExists(
	ctx context.Context,
	namespace, serviceName string,
) error {
	_, err := pc.client.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("service %s/%s not found: %w", namespace, serviceName, err)
	}

	return nil
}

// checkServiceEndpoints checks if a service has ready endpoints
func (pc *PreflightChecker) checkServiceEndpoints(
	ctx context.Context,
	namespace, serviceName string,
) error {
	endpoints, err := pc.client.CoreV1().
		Endpoints(namespace).
		Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("endpoints %s/%s not found: %w", namespace, serviceName, err)
	}

	// Check if there are any ready endpoints
	hasReady := false
	for _, subset := range endpoints.Subsets {
		if len(subset.Addresses) > 0 {
			hasReady = true
			break
		}
	}

	if !hasReady {
		return fmt.Errorf("service %s/%s has no ready endpoints", namespace, serviceName)
	}

	return nil
}
