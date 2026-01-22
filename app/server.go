package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/zijiren233/sealos-state-metric/pkg/config"
	"github.com/zijiren233/sealos-state-metric/pkg/leaderelection"
	"github.com/zijiren233/sealos-state-metric/pkg/registry"
	"github.com/zijiren233/sealos-state-metric/pkg/util"
)

// Server represents the HTTP server
type Server struct {
	config        *config.GlobalConfig
	configContent []byte
	httpServer    *http.Server
	registry      *registry.Registry
	promRegistry  *prometheus.Registry
	leaderElector *leaderelection.LeaderElector
}

// NewServer creates a new server instance
func NewServer(cfg *config.GlobalConfig, configFile string) (*Server, error) {
	var (
		configContent []byte
		err           error
	)

	// Read config file content if provided

	if configFile != "" {
		configContent, err = os.ReadFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	}

	return &Server{
		config:        cfg,
		configContent: configContent,
		registry:      registry.GetRegistry(),
		promRegistry:  prometheus.NewRegistry(),
	}, nil
}

// Run starts the server and blocks until it receives a shutdown signal
func (s *Server) Run(ctx context.Context) error {
	// Create Kubernetes client
	restConfig, client, err := util.NewKubernetesClient(
		s.config.Kubernetes.Kubeconfig,
		s.config.Kubernetes.QPS,
		s.config.Kubernetes.Burst,
	)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Initialize collectors
	if err := s.registry.Initialize(
		ctx,
		restConfig,
		client,
		s.configContent,
		s.config.Metrics.Namespace,
		s.config.Performance.InformerResyncPeriod,
		s.config.EnabledCollectors,
	); err != nil {
		return fmt.Errorf("failed to initialize collectors: %w", err)
	}

	// Register collectors with Prometheus
	promCollector := registry.NewPrometheusCollector(s.registry)
	s.promRegistry.MustRegister(promCollector)

	// Always start collectors that don't require leader election
	log.Info("Starting collectors that don't require leader election")
	// Get all collectors
	allCollectors := s.registry.ListCollectors()
	for _, name := range allCollectors {
		if c, ok := s.registry.GetCollector(name); ok {
			if !c.RequiresLeaderElection() {
				if err := c.Start(ctx); err != nil {
					log.WithError(err).
						WithField("name", name).
						Error("Failed to start non-leader collector")
				} else {
					log.WithField("name", name).Info("Non-leader collector started")
				}
			}
		}
	}

	// Setup leader election if enabled
	if s.config.LeaderElection.Enabled {
		leConfig := &leaderelection.Config{
			Namespace:     s.config.LeaderElection.Namespace,
			LeaseName:     s.config.LeaderElection.LeaseName,
			LeaseDuration: s.config.LeaderElection.LeaseDuration,
			RenewDeadline: s.config.LeaderElection.RenewDeadline,
			RetryPeriod:   s.config.LeaderElection.RetryPeriod,
		}

		leLogger := log.WithField("component", "leader-election")

		elector, err := leaderelection.NewLeaderElector(leConfig, client, leLogger)
		if err != nil {
			return fmt.Errorf("failed to create leader elector: %w", err)
		}

		// Set callbacks
		elector.SetCallbacks(
			func(ctx context.Context) {
				// OnStartedLeading: start collectors that require leader election
				log.Info("Became leader, starting leader-required collectors")

				if err := s.registry.StartWithLeaderElection(ctx, true); err != nil {
					log.WithError(err).Error("Failed to start leader-required collectors")
				}
			},
			func() {
				// OnStoppedLeading: stop collectors that require leader election
				log.Info("Lost leadership, stopping leader-required collectors")

				if err := s.registry.StopWithLeaderElection(true); err != nil {
					log.WithError(err).Error("Failed to stop leader-required collectors")
				}
			},
			func(identity string) {
				// OnNewLeader: log new leader
				log.WithField("leader", identity).Info("New leader elected")
			},
		)

		s.leaderElector = elector

		// Run leader election in background
		go func() {
			log.Info("Starting leader election")

			if err := elector.Run(ctx); err != nil {
				log.WithError(err).Error("Leader election exited with error")
			}
		}()
	} else {
		// Start all collectors directly if leader election is disabled
		log.Info("Leader election disabled, starting all collectors")

		if err := s.registry.Start(ctx); err != nil {
			return fmt.Errorf("failed to start collectors: %w", err)
		}
	}

	// Setup HTTP server
	mux := http.NewServeMux()
	s.setupRoutes(mux)

	s.httpServer = &http.Server{
		Addr:              s.config.Server.Address,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start HTTP server in background
	errChan := make(chan error, 1)
	go func() {
		log.WithField("address", s.config.Server.Address).Info("Starting HTTP server")

		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Wait for shutdown signal or error
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errChan:
		return err
	case sig := <-sigChan:
		log.WithField("signal", sig.String()).Info("Received shutdown signal")
	case <-ctx.Done():
		log.Info("Context cancelled, shutting down")
	}

	return s.Shutdown()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() error {
	log.Info("Shutting down server")

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
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	response := map[string]any{
		"status":     allHealthy,
		"collectors": healthStatus,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.WithError(err).Error("Failed to encode health response")
	}
}

// handleCollectors handles collector list requests
func (s *Server) handleCollectors(w http.ResponseWriter, r *http.Request) {
	collectors := s.registry.ListCollectors()

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(map[string]any{
		"collectors": collectors,
		"count":      len(collectors),
	}); err != nil {
		log.WithError(err).Error("Failed to encode collectors response")
	}
}

// handleLeader handles leader election status requests
func (s *Server) handleLeader(w http.ResponseWriter, r *http.Request) {
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

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.WithError(err).Error("Failed to encode leader response")
	}
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
