package server

import (
	"errors"
	"fmt"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	"github.com/labring/sealos-state-metrics/pkg/config"
	"github.com/labring/sealos-state-metrics/pkg/httpserver"
	log "github.com/sirupsen/logrus"
)

// Reload reloads the server with new configuration.
// The newConfig should be pre-loaded by the caller (e.g., via config.LoadGlobalConfig).
// This allows the caller to handle other reloads (like logger) before calling this method.
func (s *Server) Reload(newConfigContent []byte, newConfig *config.GlobalConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	logger := log.WithField("component", "config-reload")
	logger.Info("Starting server reload")

	if s.serverCtx == nil {
		return errors.New("server not running, context is nil")
	}

	// 1. Stop all collectors based on current configuration
	if err := s.stopCollectors(); err != nil {
		logger.WithError(err).Warn("Failed to stop collectors")
	}

	// Check if K8s config changed before applying new config
	k8sConfigChanged := !s.config.Kubernetes.Equal(newConfig.Kubernetes)

	// Check if Server config changed and warn if it did
	if !s.config.Server.Equal(newConfig.Server) {
		logger.Warn(
			"Server configuration changed but cannot be hot-reloaded - please restart the pod for changes to take effect",
		)
	}

	// Check if DebugServer config changed
	debugServerConfigChanged := !s.config.DebugServer.Equal(newConfig.DebugServer)

	// Apply new config (buildInitConfig uses s.config)
	s.config.ApplyHotReload(newConfig)
	s.configContent = newConfigContent

	// Reload debug server if config changed
	if debugServerConfigChanged {
		logger.Info("Debug server configuration changed, reloading debug server")

		if err := s.reloadDebugServer(); err != nil {
			logger.WithError(err).Error("Failed to reload debug server")
			// Don't fail the entire reload if debug server fails
		}
	}

	// Recreate client provider if K8s config changed
	// This will cause the client to be re-initialized with new config when needed
	if k8sConfigChanged {
		logger.Info("Kubernetes configuration changed, creating new client provider")

		s.clientProvider = collector.NewClientProvider(
			collector.ClientConfig{
				Kubeconfig: s.config.Kubernetes.Kubeconfig,
				QPS:        s.config.Kubernetes.QPS,
				Burst:      s.config.Kubernetes.Burst,
			},
			log.WithField("component", "client-provider"),
		)
	}

	// 3. Reinitialize and start collectors atomically, and setup leader election if needed
	// This is done atomically to minimize the gap where collectors are running
	// but leader election is not yet set up
	if err := s.reinitializeAndStartCollectors(); err != nil {
		return fmt.Errorf("failed to reinitialize collectors: %w", err)
	}

	logger.Info("Server reload completed successfully")

	return nil
}

// reloadDebugServer reloads the debug HTTP server with new configuration
func (s *Server) reloadDebugServer() error {
	// Stop existing debug server if running
	if s.debugServer != nil {
		if err := s.debugServer.Stop(); err != nil {
			log.WithError(err).Warn("Failed to stop debug server during reload")
		}

		s.debugServer = nil
	}

	// Start new debug server if enabled
	if s.config.DebugServer.Enabled {
		debugHandler, err := s.createDebugHandler()
		if err != nil {
			return fmt.Errorf("failed to create debug handler: %w", err)
		}

		s.debugServer = httpserver.New(httpserver.Config{
			Address: fmt.Sprintf("127.0.0.1:%d", s.config.DebugServer.Port),
			Handler: debugHandler,
			Name:    "debug",
		})

		if err := s.debugServer.Start(s.serverCtx); err != nil {
			return fmt.Errorf("failed to start debug server: %w", err)
		}

		log.Info("Debug server reloaded successfully")
	} else {
		log.Info("Debug server disabled")
	}

	return nil
}
