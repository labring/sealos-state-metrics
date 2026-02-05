// Package identity provides instance identity detection.
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"os"
	"sync"
)

var (
	// once ensures identity is computed only once
	once sync.Once
	// instance stores the computed identity
	instance string
)

// Get returns the global instance identity.
// It uses sync.Once to ensure it's only computed once.
// Priority: explicit config > POD_NAME env var > hostname
func Get() string {
	once.Do(func() {
		instance = compute()
	})
	return instance
}

// GetWithConfig returns the instance identity with configuration override.
// Priority: configIdentity > nodeName > podName > auto-detected (POD_NAME env > IP > hostname > random)
func GetWithConfig(configIdentity, nodeName, podName string) string {
	if configIdentity != "" {
		return configIdentity
	}

	// Try NODE_NAME first (for DaemonSet deployments)
	if nodeName != "" {
		return nodeName
	}

	// Try POD_NAME parameter
	if podName != "" {
		return podName
	}

	return Get()
}

// compute determines the instance identity from environment or system
func compute() string {
	// Priority 1: NODE_NAME environment variable (for DaemonSet)
	if nodeName := os.Getenv("NODE_NAME"); nodeName != "" {
		return nodeName
	}

	// Priority 2: POD_NAME environment variable
	if podName := os.Getenv("POD_NAME"); podName != "" {
		return podName
	}

	// Priority 3: First non-loopback IP address
	if ip := getOutboundIP(); ip != "" {
		return ip
	}

	// Priority 4: Hostname
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		return hostname
	}

	// Priority 5: Random identifier
	return generateRandomID()
}

// getOutboundIP returns the first non-loopback IP address
func getOutboundIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return ipNet.IP.String()
			}
		}
	}

	return ""
}

// generateRandomID generates a random identifier
func generateRandomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}

	return "instance-" + hex.EncodeToString(b)
}

// Reset resets the cached identity (only for testing)
func Reset() {
	once = sync.Once{}
	instance = ""
}
