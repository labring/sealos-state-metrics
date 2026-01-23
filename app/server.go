package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/zijiren233/sealos-state-metric/pkg/auth"
	"github.com/zijiren233/sealos-state-metric/pkg/config"
	"github.com/zijiren233/sealos-state-metric/pkg/identity"
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
	debugServer   *http.Server
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

	// Debug server management
	debugListener   net.Listener
	debugCtx        context.Context
	debugCtxCancel  context.CancelFunc
	debugMu         sync.Mutex
	debugServerDone chan struct{}
}

// ReloadAwareCollector wraps a prometheus.Collector and blocks operations during reload
type ReloadAwareCollector struct {
	server *Server
	inner  prometheus.Collector
}

// certCache caches TLS certificate with fsnotify-based reloading
type certCache struct {
	mu       sync.RWMutex
	cert     *tls.Certificate
	certFile string
	keyFile  string
	watcher  *fsnotify.Watcher
	stopCh   chan struct{}
}

// newCertCache creates a new certificate cache with file watching
func newCertCache(certFile, keyFile string) (*certCache, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	c := &certCache{
		certFile: certFile,
		keyFile:  keyFile,
		watcher:  watcher,
		stopCh:   make(chan struct{}),
	}

	// Load initial certificate
	if err := c.loadCertificate(); err != nil {
		watcher.Close()
		return nil, err
	}

	// Start watching files
	if err := c.startWatching(); err != nil {
		watcher.Close()
		return nil, err
	}

	return c, nil
}

// startWatching starts watching certificate files for changes
func (c *certCache) startWatching() error {
	if err := c.watcher.Add(c.certFile); err != nil {
		return fmt.Errorf("failed to watch cert file: %w", err)
	}

	if err := c.watcher.Add(c.keyFile); err != nil {
		return fmt.Errorf("failed to watch key file: %w", err)
	}

	// Start watch goroutine
	go c.watchLoop()

	// Also poll periodically as backup (every 10s, same as controller-runtime)
	go c.pollLoop()

	log.WithFields(log.Fields{
		"certFile": c.certFile,
		"keyFile":  c.keyFile,
	}).Info("Started certificate file watcher")

	return nil
}

// watchLoop watches for file system events
func (c *certCache) watchLoop() {
	for {
		select {
		case event, ok := <-c.watcher.Events:
			if !ok {
				return
			}

			// React to Write, Create, Chmod, or Remove events
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Chmod|fsnotify.Remove) != 0 {
				log.WithField("event", event).Debug("Certificate file changed")

				if err := c.loadCertificate(); err != nil {
					log.WithError(err).Error("Failed to reload certificate")
				}

				// Re-add watch if file was removed/renamed
				if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
					if err := c.watcher.Add(event.Name); err != nil {
						log.WithError(err).Error("Failed to re-add watch")
					}
				}
			}

		case err, ok := <-c.watcher.Errors:
			if !ok {
				return
			}

			log.WithError(err).Warn("Certificate watcher error")

		case <-c.stopCh:
			return
		}
	}
}

// pollLoop periodically checks for certificate changes (backup mechanism)
func (c *certCache) pollLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := c.loadCertificate(); err != nil {
				log.WithError(err).Debug("Failed to reload certificate during poll")
			}
		case <-c.stopCh:
			return
		}
	}
}

// loadCertificate loads certificate from disk
func (c *certCache) loadCertificate() error {
	certPEM, err := os.ReadFile(c.certFile)
	if err != nil {
		return err
	}

	keyPEM, err := os.ReadFile(c.keyFile)
	if err != nil {
		return err
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return err
	}

	c.mu.Lock()
	oldCert := c.cert
	c.cert = &cert
	c.mu.Unlock()

	// Only log if certificate actually changed
	if oldCert == nil || !equalCerts(oldCert, &cert) {
		log.Info("TLS certificate reloaded")
	}

	return nil
}

// getCertificate returns cached certificate (for tls.Config.GetCertificate)
func (c *certCache) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.cert == nil {
		return nil, errors.New("no certificate loaded")
	}

	return c.cert, nil
}

// stop stops the certificate watcher
func (c *certCache) stop() {
	close(c.stopCh)
	c.watcher.Close()
}

// equalCerts checks if two certificates are equal
func equalCerts(a, b *tls.Certificate) bool {
	if len(a.Certificate) != len(b.Certificate) {
		return false
	}

	if len(a.Certificate) == 0 {
		return true
	}
	// Compare first certificate in chain
	return string(a.Certificate[0]) == string(b.Certificate[0])
}

// Describe implements prometheus.Collector
func (rc *ReloadAwareCollector) Describe(ch chan<- *prometheus.Desc) {
	// Hold server read lock - allows concurrent describes but blocks during reload
	rc.server.mu.RLock()
	defer rc.server.mu.RUnlock()

	// Delegate to inner collector
	rc.inner.Describe(ch)
}

// Collect implements prometheus.Collector
func (rc *ReloadAwareCollector) Collect(ch chan<- prometheus.Metric) {
	// Hold server read lock - allows concurrent collection but blocks during reload
	rc.server.mu.RLock()
	defer rc.server.mu.RUnlock()

	// Delegate to inner collector
	rc.inner.Collect(ch)
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

// Serve starts the HTTP server and blocks until shutdown
func (s *Server) Serve() error {
	// Create main server listener
	listener, err := s.createListener()
	if err != nil {
		return err
	}
	defer listener.Close()

	wrappedListener, err := s.wrapListenerWithTLS(listener)
	if err != nil {
		return err
	}

	s.httpServer = &http.Server{
		Handler:           s.createHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start debug server if enabled
	if s.config.DebugServer.Enabled {
		if err := s.startDebugServer(); err != nil {
			return fmt.Errorf("failed to start debug server: %w", err)
		}
	}

	return s.serveWithContext(wrappedListener)
}

// createListener creates and returns a TCP listener
func (s *Server) createListener() (net.Listener, error) {
	lc := net.ListenConfig{}

	listener, err := lc.Listen(s.serverCtx, "tcp", s.config.Server.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to create listener on %s: %w", s.config.Server.Address, err)
	}

	return listener, nil
}

// createHandler creates and configures the HTTP handler
func (s *Server) createHandler() http.Handler {
	mux := http.NewServeMux()
	s.setupRoutes(mux)
	return mux
}

// createDebugHandler creates HTTP handler for debug server (no auth)
func (s *Server) createDebugHandler() http.Handler {
	mux := http.NewServeMux()
	s.setupDebugRoutes(mux)
	return mux
}

// wrapListenerWithTLS wraps listener with TLS if enabled
func (s *Server) wrapListenerWithTLS(listener net.Listener) (net.Listener, error) {
	if !s.config.Server.TLS.Enabled {
		log.WithField("address", listener.Addr().String()).Info("Starting HTTP server")
		return listener, nil
	}

	// Create certificate cache with file watching
	cache, err := newCertCache(s.config.Server.TLS.CertFile, s.config.Server.TLS.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate watcher: %w", err)
	}

	// Verify certificate is loaded
	if _, err := cache.getCertificate(nil); err != nil {
		cache.stop()
		return nil, fmt.Errorf("failed to load TLS certificate at startup: %w", err)
	}

	log.WithFields(log.Fields{
		"address":  listener.Addr().String(),
		"certFile": s.config.Server.TLS.CertFile,
		"keyFile":  s.config.Server.TLS.KeyFile,
	}).Info("Starting HTTPS server with TLS (certificate auto-reload enabled via fsnotify)")

	tlsConfig := &tls.Config{
		GetCertificate: cache.getCertificate,
		MinVersion:     tls.VersionTLS12,
	}

	return tls.NewListener(listener, tlsConfig), nil
}

// serveWithContext starts the HTTP server(s) and waits for shutdown
func (s *Server) serveWithContext(listener net.Listener) error {
	errChan := make(chan error, 1)

	// Start main server
	go func() {
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	select {
	case err := <-errChan:
		return err
	case <-s.serverCtx.Done():
		log.Info("Context cancelled, shutting down")
		return s.Shutdown()
	}
}

// startCollectors starts collectors with or without leader election
func (s *Server) startCollectors() error {
	if !s.config.LeaderElection.Enabled {
		log.Info("Leader election disabled, starting all collectors")
		return s.registry.Start(s.serverCtx)
	}

	// Start non-leader collectors immediately
	if err := s.registry.StartNonLeaderCollectors(s.serverCtx); err != nil {
		log.WithError(err).Warn("Some non-leader collectors failed to start")
	}

	// Setup leader election
	return s.setupLeaderElection()
}

// stopCollectors stops all collectors based on current leader election configuration
func (s *Server) stopCollectors() error {
	logger := log.WithField("component", "server")

	if s.config.LeaderElection.Enabled {
		// Current state: leader election is enabled
		// Stop leader election first (will trigger OnStoppedLeading callback to stop leader collectors)
		s.stopLeaderElection()
		// Then stop non-leader collectors
		if err := s.registry.StopNonLeaderCollectors(); err != nil {
			logger.WithError(err).Warn("Failed to stop non-leader collectors")
			return err
		}
	} else {
		// Current state: leader election is disabled
		// All collectors were started without leader election, stop them all
		if err := s.registry.Stop(); err != nil {
			logger.WithError(err).Warn("Failed to stop collectors")
			return err
		}
	}

	return nil
}

// reinitializeAndStartCollectors reinitializes collectors and sets up leader election.
// IMPORTANT: Caller (Reload) must hold s.mu lock.
func (s *Server) reinitializeAndStartCollectors() error {
	// Reinitialize collectors (creates new collector instances)
	if err := s.registry.Reinitialize(s.buildInitConfig()); err != nil {
		return fmt.Errorf("failed to reinitialize collectors: %w", err)
	}

	// Start collectors with new configuration
	if err := s.startCollectors(); err != nil {
		return fmt.Errorf("failed to start collectors: %w", err)
	}

	return nil
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

			if err := s.registry.StartLeaderCollectors(ctx); err != nil {
				log.WithError(err).Error("Failed to start leader-required collectors")
			}
		},
		func() {
			log.Info("Lost leadership, stopping leader-required collectors")

			if err := s.registry.StopLeaderCollectors(); err != nil {
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

	// Recreate Kubernetes client if config changed
	if k8sConfigChanged {
		logger.Info("Kubernetes configuration changed, recreating client")

		if err := s.initKubernetesClient(s.config.Kubernetes); err != nil {
			return err
		}
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

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() error {
	log.Info("Shutting down server")

	// 1. Shutdown HTTP servers first - stop accepting new requests but wait for existing ones
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Shutdown main HTTP server
	if err := s.httpServer.Shutdown(ctx); err != nil {
		log.WithError(err).Error("Failed to shutdown HTTP server gracefully")
	}

	// Shutdown debug HTTP server if running
	if err := s.stopDebugServer(); err != nil {
		log.WithError(err).Error("Failed to shutdown debug server gracefully")
	}

	// 2. Stop all collectors based on current configuration
	if err := s.stopCollectors(); err != nil {
		log.WithError(err).Error("Failed to stop collectors")
	}

	log.Info("Server shutdown complete")

	return nil
}

// startDebugServer starts the debug HTTP server in a goroutine
func (s *Server) startDebugServer() error {
	s.debugMu.Lock()
	defer s.debugMu.Unlock()

	// Create debug listener
	lc := net.ListenConfig{}

	listener, err := lc.Listen(s.serverCtx, "tcp", s.config.DebugServer.Address)
	if err != nil {
		return fmt.Errorf("failed to create debug listener on %s: %w", s.config.DebugServer.Address, err)
	}

	s.debugListener = listener

	// Create debug server
	s.debugServer = &http.Server{
		Handler:           s.createDebugHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Create context for debug server
	s.debugCtx, s.debugCtxCancel = context.WithCancel(s.serverCtx)
	s.debugServerDone = make(chan struct{})

	log.WithField("address", listener.Addr().String()).Info("Starting debug HTTP server (no auth)")

	// Start server in goroutine
	go func() {
		defer close(s.debugServerDone)

		if err := s.debugServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Error("Debug server error")
		}
	}()

	return nil
}

// stopDebugServer stops the debug HTTP server
func (s *Server) stopDebugServer() error {
	s.debugMu.Lock()
	defer s.debugMu.Unlock()

	if s.debugServer == nil {
		return nil
	}

	log.Info("Stopping debug HTTP server")

	// Cancel debug context
	if s.debugCtxCancel != nil {
		s.debugCtxCancel()
	}

	// Shutdown server gracefully
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.debugServer.Shutdown(ctx); err != nil {
		log.WithError(err).Warn("Failed to shutdown debug server gracefully, forcing close")
		// Force close listener
		if s.debugListener != nil {
			s.debugListener.Close()
		}
	}

	// Wait for server goroutine to exit
	if s.debugServerDone != nil {
		<-s.debugServerDone
	}

	// Clean up
	s.debugServer = nil
	s.debugListener = nil
	s.debugCtx = nil
	s.debugCtxCancel = nil
	s.debugServerDone = nil

	log.Info("Debug HTTP server stopped")

	return nil
}

// reloadDebugServer reloads the debug HTTP server with new configuration
func (s *Server) reloadDebugServer() error {
	// Stop existing debug server if running
	if err := s.stopDebugServer(); err != nil {
		return fmt.Errorf("failed to stop debug server: %w", err)
	}

	// Start new debug server if enabled
	if s.config.DebugServer.Enabled {
		if err := s.startDebugServer(); err != nil {
			return fmt.Errorf("failed to start debug server: %w", err)
		}

		log.Info("Debug server reloaded successfully")
	} else {
		log.Info("Debug server disabled")
	}

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
	// Metrics endpoint with optional authentication
	metricsHandler := promhttp.HandlerFor(
		s.promRegistry,
		promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		},
	)

	// Apply authentication middleware if enabled
	if s.config.Server.Auth.Enabled {
		authenticator := auth.NewAuthenticator(s.client)
		metricsHandler = authenticator.Middleware(metricsHandler)

		log.Info("Kubernetes authentication enabled for metrics endpoint")
	}

	mux.Handle(s.config.Server.MetricsPath, metricsHandler)

	// Health endpoint (no authentication)
	mux.HandleFunc(s.config.Server.HealthPath, s.handleHealth)

	// Collectors list endpoint (no authentication)
	mux.HandleFunc("/collectors", s.handleCollectors)

	// Leader election endpoint (no authentication)
	mux.HandleFunc("/leader", s.handleLeader)

	// Root endpoint (no authentication)
	mux.HandleFunc("/", s.handleRoot)
}

// setupDebugRoutes configures HTTP routes for debug server (no authentication)
func (s *Server) setupDebugRoutes(mux *http.ServeMux) {
	// Metrics endpoint (no authentication)
	metricsHandler := promhttp.HandlerFor(
		s.promRegistry,
		promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		},
	)
	mux.Handle(s.config.DebugServer.MetricsPath, metricsHandler)

	// Health endpoint (no authentication)
	mux.HandleFunc(s.config.DebugServer.HealthPath, s.handleHealth)

	// Collectors list endpoint (no authentication)
	mux.HandleFunc("/collectors", s.handleCollectors)

	// Leader election endpoint (no authentication)
	mux.HandleFunc("/leader", s.handleLeader)

	// Root endpoint (no authentication)
	mux.HandleFunc("/", s.handleRoot)
}

// handleHealth handles health check requests
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	// Get all collectors
	allCollectors := s.registry.GetAllCollectors()

	// Determine which collectors should be checked based on leader election state
	healthStatus := make(map[string]error)
	allHealthy := true

	for name, c := range allCollectors {
		// Determine if this collector should be checked
		var shouldCheck bool

		if !s.config.LeaderElection.Enabled {
			// Leader election disabled: all collectors should be running
			shouldCheck = true
		} else {
			// Leader election enabled
			if c.RequiresLeaderElection() {
				// Leader-required collector: only check if we are the leader
				s.leMu.Lock()
				isLeader := s.leaderElector != nil && s.leaderElector.IsLeader()
				s.leMu.Unlock()

				shouldCheck = isLeader
			} else {
				// Non-leader collector: always check (should always be running)
				shouldCheck = true
			}
		}

		if shouldCheck {
			err := c.Health()

			healthStatus[name] = err
			if err != nil {
				allHealthy = false
			}
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
