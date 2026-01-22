package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/zijiren233/sealos-state-metric/pkg/config"
	"github.com/zijiren233/sealos-state-metric/pkg/leaderelection"
	"github.com/zijiren233/sealos-state-metric/pkg/registry"
	"github.com/zijiren233/sealos-state-metric/pkg/util"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Server represents the HTTP server
type Server struct {
	config        *config.GlobalConfig
	configContent []byte
	httpServer    *http.Server
	registry      *registry.Registry
	promRegistry  *prometheus.Registry
	leaderElector *leaderelection.LeaderElector

	// Fields needed for reinitialization
	mu sync.Mutex
	//nolint:containedctx // Context stored for reload functionality
	serverCtx  context.Context
	restConfig *rest.Config
	client     kubernetes.Interface

	// Leader election management
	leCtxCancel context.CancelFunc
	leDoneCh    chan struct{} // Closed when leader election goroutine exits
	leMu        sync.Mutex
}

// NewServer creates a new server instance
func NewServer(cfg *config.GlobalConfig, configContent []byte) *Server {
	return &Server{
		config:        cfg,
		configContent: configContent,
		registry:      registry.GetRegistry(),
		promRegistry:  prometheus.NewRegistry(),
	}
}

// Run starts the server and blocks until it receives a shutdown signal
func (s *Server) Run(ctx context.Context) error {
	// Initialize server
	if err := s.init(ctx); err != nil {
		return fmt.Errorf("failed to initialize server: %w", err)
	}
	// Start HTTP server and wait for shutdown
	return s.serveAndWait()
}

func (s *Server) init(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.serverCtx = ctx

	// Initialize Kubernetes client and collectors
	if err := s.initKubernetesClient(s.config.Kubernetes); err != nil {
		return err
	}

	if err := s.registry.Initialize(s.buildInitConfig()); err != nil {
		return fmt.Errorf("failed to initialize collectors: %w", err)
	}

	// Register collectors with Prometheus
	s.promRegistry.MustRegister(registry.NewPrometheusCollector(s.registry))

	// Start collectors (with or without leader election)
	return s.startCollectors()
}

// initKubernetesClient creates and stores the Kubernetes client
func (s *Server) initKubernetesClient(cfg config.KubernetesConfig) error {
	restConfig, client, err := util.NewKubernetesClient(cfg.Kubeconfig, cfg.QPS, cfg.Burst)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	s.restConfig = restConfig
	s.client = client

	return nil
}

// buildInitConfig creates registry.InitConfig from current server state
func (s *Server) buildInitConfig() *registry.InitConfig {
	return &registry.InitConfig{
		Ctx:                  s.serverCtx,
		RestConfig:           s.restConfig,
		Client:               s.client,
		ConfigContent:        s.configContent,
		MetricsNamespace:     s.config.Metrics.Namespace,
		InformerResyncPeriod: s.config.Performance.InformerResyncPeriod,
		EnabledCollectors:    s.config.EnabledCollectors,
		Identity:             s.config.Identity,
	}
}

// buildLeaderElectionConfig creates leaderelection.Config from current server state
func (s *Server) buildLeaderElectionConfig() *leaderelection.Config {
	return &leaderelection.Config{
		Namespace:     s.config.LeaderElection.Namespace,
		LeaseName:     s.config.LeaderElection.LeaseName,
		Identity:      s.config.Identity,
		LeaseDuration: s.config.LeaderElection.LeaseDuration,
		RenewDeadline: s.config.LeaderElection.RenewDeadline,
		RetryPeriod:   s.config.LeaderElection.RetryPeriod,
	}
}

// serveAndWait starts the HTTP server and waits for context cancellation
func (s *Server) serveAndWait() error {
	mux := http.NewServeMux()
	s.setupRoutes(mux)

	s.httpServer = &http.Server{
		Addr:              s.config.Server.Address,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errChan := make(chan error, 1)
	go func() {
		log.WithField("address", s.config.Server.Address).Info("Starting HTTP server")

		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	select {
	case err := <-errChan:
		return err
	case <-s.serverCtx.Done():
		log.Info("Context cancelled, shutting down")
	}

	return s.Shutdown()
}

// startCollectors starts collectors with or without leader election
func (s *Server) startCollectors() error {
	if !s.config.LeaderElection.Enabled {
		log.Info("Leader election disabled, starting all collectors")
		return s.registry.Start(s.serverCtx)
	}

	// Start non-leader collectors immediately
	if err := s.registry.StartWithLeaderElection(s.serverCtx, false); err != nil {
		log.WithError(err).Warn("Some non-leader collectors failed to start")
	}

	// Setup leader election
	return s.setupLeaderElection()
}

// setupLeaderElection creates and starts the leader elector
func (s *Server) setupLeaderElection() error {
	elector, err := leaderelection.NewLeaderElector(
		s.buildLeaderElectionConfig(),
		s.client,
		log.WithField("component", "leader-election"),
	)
	if err != nil {
		return fmt.Errorf("failed to create leader elector: %w", err)
	}

	elector.SetCallbacks(
		func(ctx context.Context) {
			log.Info("Became leader, starting leader-required collectors")

			if err := s.registry.StartWithLeaderElection(ctx, true); err != nil {
				log.WithError(err).Error("Failed to start leader-required collectors")
			}
		},
		func() {
			log.Info("Lost leadership, stopping leader-required collectors")

			if err := s.registry.StopWithLeaderElection(true); err != nil {
				log.WithError(err).Error("Failed to stop leader-required collectors")
			}
		},
		func(identity string) {
			log.WithField("leader", identity).Info("New leader elected")
		},
	)

	// Create cancellable context and done channel for cleanup
	s.leMu.Lock()
	defer s.leMu.Unlock()
	leCtx, leCtxCancel := context.WithCancel(s.serverCtx)
	s.leCtxCancel = leCtxCancel
	s.leDoneCh = make(chan struct{})
	s.leaderElector = elector

	go func() {
		defer close(s.leDoneCh)

		log.Info("Starting leader election")

		if err := elector.Run(leCtx); err != nil {
			log.WithError(err).Error("Leader election exited with error")
		}

		log.Info("Leader election stopped")
	}()

	return nil
}

// stopLeaderElection stops the current leader election and releases the lease
func (s *Server) stopLeaderElection() {
	s.leMu.Lock()
	defer s.leMu.Unlock()
	leCtxCancel := s.leCtxCancel
	leDoneCh := s.leDoneCh

	if leCtxCancel != nil {
		log.Info("Stopping leader election and releasing lease")
		leCtxCancel()

		// Wait for leader election goroutine to exit
		if leDoneCh != nil {
			<-leDoneCh
		}

		s.leCtxCancel = nil
		s.leDoneCh = nil
		s.leaderElector = nil
	}
}

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

	// Stop current leader election
	s.stopLeaderElection()

	// Check if K8s config changed before applying new config
	k8sConfigChanged := !s.config.Kubernetes.Equal(newConfig.Kubernetes)

	// Apply new config (buildInitConfig uses s.config)
	s.config.ApplyHotReload(newConfig)
	s.configContent = newConfigContent

	// Recreate Kubernetes client if config changed
	if k8sConfigChanged {
		logger.Info("Kubernetes configuration changed, recreating client")

		if err := s.initKubernetesClient(s.config.Kubernetes); err != nil {
			return err
		}
	}

	// Reinitialize and restart collectors
	if err := s.registry.Reinitialize(s.buildInitConfig()); err != nil {
		return fmt.Errorf("failed to reinitialize collectors: %w", err)
	}

	if err := s.startCollectors(); err != nil {
		return fmt.Errorf("failed to start collectors: %w", err)
	}

	logger.Info("Server reload completed successfully")

	return nil
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() error {
	log.Info("Shutting down server")

	// Stop leader election (idempotent)
	s.stopLeaderElection()

	// Stop collectors
	if err := s.registry.Stop(); err != nil {
		log.WithError(err).Error("Failed to stop collectors")
	}

	// Shutdown HTTP server with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown HTTP server: %w", err)
	}

	log.Info("Server shutdown complete")

	return nil
}

// writeJSON writes a JSON response with the given status code
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.WithError(err).Error("Failed to encode JSON response")
	}
}

// setupRoutes configures HTTP routes
func (s *Server) setupRoutes(mux *http.ServeMux) {
	// Metrics endpoint
	mux.Handle(s.config.Server.MetricsPath, promhttp.HandlerFor(
		s.promRegistry,
		promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		},
	))

	// Health endpoint
	mux.HandleFunc(s.config.Server.HealthPath, s.handleHealth)

	// Collectors list endpoint
	mux.HandleFunc("/collectors", s.handleCollectors)

	// Leader election endpoint
	mux.HandleFunc("/leader", s.handleLeader)

	// Root endpoint
	mux.HandleFunc("/", s.handleRoot)
}

// handleHealth handles health check requests
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	healthStatus := s.registry.HealthCheck()

	allHealthy := true
	for _, err := range healthStatus {
		if err != nil {
			allHealthy = false
			break
		}
	}

	status := http.StatusOK
	if !allHealthy {
		status = http.StatusServiceUnavailable
	}

	writeJSON(w, status, map[string]any{
		"status":     allHealthy,
		"collectors": healthStatus,
	})
}

// handleCollectors handles collector list requests
func (s *Server) handleCollectors(w http.ResponseWriter, _ *http.Request) {
	collectors := s.registry.ListCollectors()
	writeJSON(w, http.StatusOK, map[string]any{
		"collectors": collectors,
		"count":      len(collectors),
	})
}

// handleLeader handles leader election status requests
func (s *Server) handleLeader(w http.ResponseWriter, _ *http.Request) {
	response := map[string]any{
		"enabled": s.config.LeaderElection.Enabled,
	}

	if s.leaderElector != nil {
		response["isLeader"] = s.leaderElector.IsLeader()
		response["currentLeader"] = s.leaderElector.GetLeader()
		response["identity"] = s.leaderElector.GetIdentity()
	} else {
		response["isLeader"] = true
		response["message"] = "Leader election disabled"
	}

	writeJSON(w, http.StatusOK, response)
}

// handleRoot handles root requests
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
	<title>Sealos State Metric</title>
	<style>
		body { font-family: Arial, sans-serif; margin: 40px; }
		h1 { color: #333; }
		a { color: #0066cc; text-decoration: none; margin-right: 20px; }
		a:hover { text-decoration: underline; }
		.info { background: #f0f0f0; padding: 15px; border-radius: 5px; margin-top: 20px; }
	</style>
</head>
<body>
	<h1>Sealos State Metric</h1>
	<p>Production-grade Kubernetes cluster state monitoring system</p>
	<div>
		<a href="%s">Metrics</a>
		<a href="%s">Health</a>
		<a href="/collectors">Collectors</a>
	</div>
</body>
</html>
`, s.config.Server.MetricsPath, s.config.Server.HealthPath)
}
