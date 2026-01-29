// Package server provides the main HTTP server for sealos-state-metrics
package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"

	"github.com/labring/sealos-state-metrics/pkg/config"
	"github.com/labring/sealos-state-metrics/pkg/httpserver"
	"github.com/labring/sealos-state-metrics/pkg/identity"
	"github.com/labring/sealos-state-metrics/pkg/leaderelection"
	"github.com/labring/sealos-state-metrics/pkg/registry"
	"github.com/labring/sealos-state-metrics/pkg/tlscache"
	"github.com/labring/sealos-state-metrics/pkg/util"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Server represents the HTTP server
type Server struct {
	config        *config.GlobalConfig
	configContent []byte
	mainServer    *httpserver.Server
	debugServer   *httpserver.Server
	registry      *registry.Registry
	promRegistry  *prometheus.Registry
	leaderElector *leaderelection.LeaderElector

	// Fields needed for reinitialization
	mu sync.RWMutex // Protects reload operations; readers (Collect) use RLock, writers (Reload) use Lock
	//nolint:containedctx // Context stored for reload functionality
	serverCtx  context.Context
	restConfig *rest.Config
	client     kubernetes.Interface

	// Leader election management
	leCtxCancel context.CancelFunc
	leDoneCh    chan struct{} // Closed when leader election goroutine exits
	leMu        sync.Mutex
}

// New creates a new server instance
func New(cfg *config.GlobalConfig, configContent []byte) *Server {
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
	if err := s.Init(ctx); err != nil {
		return fmt.Errorf("failed to initialize server: %w", err)
	}
	// Start HTTP server and wait for shutdown
	return s.Serve()
}

// Init initializes the server (Kubernetes client, collectors, HTTP server)
// This method is exported to allow external control of initialization timing
func (s *Server) Init(ctx context.Context) error {
	s.serverCtx = ctx

	// Initialize Kubernetes client and collectors
	if err := s.initKubernetesClient(s.config.Kubernetes); err != nil {
		return err
	}

	if err := s.registry.Initialize(s.buildInitConfig()); err != nil {
		return fmt.Errorf("failed to initialize collectors: %w", err)
	}

	// Register collectors with Prometheus wrapped by ReloadAwareCollector
	// This ensures metrics collection is blocked during reload operations
	innerCollector := registry.NewPrometheusCollector(s.registry, s.config.Metrics.Namespace)
	wrappedCollector := &ReloadAwareCollector{
		server: s,
		inner:  innerCollector,
	}
	s.promRegistry.MustRegister(wrappedCollector)

	// Start collectors (with or without leader election)
	// Note: This may take several seconds waiting for informer cache sync
	return s.startCollectors()
}

// Serve starts the HTTP server and blocks until shutdown
func (s *Server) Serve() error {
	// Create TLS config if enabled
	var tlsConfig *tls.Config
	if s.config.Server.TLS.Enabled {
		cache, err := tlscache.New(s.config.Server.TLS.CertFile, s.config.Server.TLS.KeyFile)
		if err != nil {
			return fmt.Errorf("failed to create TLS certificate cache: %w", err)
		}

		// Verify certificate is loaded
		if _, err := cache.GetCertificate(nil); err != nil {
			cache.Stop()
			return fmt.Errorf("failed to load TLS certificate at startup: %w", err)
		}

		tlsConfig = &tls.Config{
			GetCertificate: cache.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		}

		log.WithFields(log.Fields{
			"certFile": s.config.Server.TLS.CertFile,
			"keyFile":  s.config.Server.TLS.KeyFile,
		}).Info("TLS enabled with certificate auto-reload via fsnotify")
	}

	// Create main HTTP server
	s.mainServer = httpserver.New(httpserver.Config{
		Address:   s.config.Server.Address,
		Handler:   s.createMainHandler(),
		TLSConfig: tlsConfig,
		Name:      "main",
	})

	if err := s.mainServer.Start(s.serverCtx); err != nil {
		return fmt.Errorf("failed to start main server: %w", err)
	}

	// Start debug server if enabled
	if s.config.DebugServer.Enabled {
		s.debugServer = httpserver.New(httpserver.Config{
			Address: fmt.Sprintf("127.0.0.1:%d", s.config.DebugServer.Port),
			Handler: s.createDebugHandler(),
			Name:    "debug",
		})

		if err := s.debugServer.Start(s.serverCtx); err != nil {
			return fmt.Errorf("failed to start debug server: %w", err)
		}
	}

	// Wait for context cancellation
	<-s.serverCtx.Done()
	log.Info("Context cancelled, shutting down")

	return s.Shutdown()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() error {
	log.Info("Shutting down server")

	// 1. Shutdown HTTP servers
	if s.mainServer != nil {
		if err := s.mainServer.Stop(); err != nil {
			log.WithError(err).Error("Failed to shutdown main HTTP server")
		}
	}

	if s.debugServer != nil {
		if err := s.debugServer.Stop(); err != nil {
			log.WithError(err).Error("Failed to shutdown debug HTTP server")
		}
	}

	// 2. Stop all collectors
	if err := s.stopCollectors(); err != nil {
		log.WithError(err).Error("Failed to stop collectors")
	}

	log.Info("Server shutdown complete")

	return nil
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
		Identity:             s.config.Identity,
		NodeName:             s.config.NodeName,
		PodName:              s.config.PodName,
		MetricsNamespace:     s.config.Metrics.Namespace,
		InformerResyncPeriod: s.config.Performance.InformerResyncPeriod,
		EnabledCollectors:    s.config.EnabledCollectors,
	}
}

// buildLeaderElectionConfig creates leaderelection.Config from current server state
func (s *Server) buildLeaderElectionConfig() *leaderelection.Config {
	return &leaderelection.Config{
		Namespace: s.config.LeaderElection.Namespace,
		LeaseName: s.config.LeaderElection.LeaseName,
		Identity: identity.GetWithConfig(
			s.config.Identity,
			s.config.NodeName,
			s.config.PodName,
		),
		LeaseDuration: s.config.LeaderElection.LeaseDuration,
		RenewDeadline: s.config.LeaderElection.RenewDeadline,
		RetryPeriod:   s.config.LeaderElection.RetryPeriod,
	}
}

// createMainHandler creates the HTTP handler for main server (with optional auth)
func (s *Server) createMainHandler() http.Handler {
	mux := http.NewServeMux()
	s.setupRoutes(
		mux,
		s.config.Server.MetricsPath,
		s.config.Server.HealthPath,
		s.config.Server.Auth.Enabled,
	)

	return mux
}

// createDebugHandler creates HTTP handler for debug server (no auth)
func (s *Server) createDebugHandler() http.Handler {
	mux := http.NewServeMux()
	s.setupRoutes(mux, s.config.DebugServer.MetricsPath, s.config.DebugServer.HealthPath, false)
	return mux
}
