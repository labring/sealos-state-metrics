// Package httpserver provides a reusable HTTP server with optional TLS and graceful shutdown
package httpserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Config contains HTTP server configuration
type Config struct {
	// Address to listen on (e.g., ":8080")
	Address string
	// Handler is the HTTP handler
	Handler http.Handler
	// TLSConfig is optional TLS configuration
	TLSConfig *tls.Config
	// ReadHeaderTimeout for the HTTP server
	ReadHeaderTimeout time.Duration
	// Name for logging (e.g., "main", "debug", "pprof")
	Name string
	// Logger is used for server lifecycle logs.
	Logger *log.Entry
}

// Server manages an HTTP server with graceful shutdown
type Server struct {
	config   Config
	server   *http.Server
	listener net.Listener
	//nolint:containedctx // Context stored for server lifecycle management
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
}

// New creates a new HTTP server with the given configuration
func New(config Config) *Server {
	// Set defaults
	if config.ReadHeaderTimeout == 0 {
		config.ReadHeaderTimeout = 10 * time.Second
	}

	if config.Name == "" {
		config.Name = "http"
	}

	return &Server{
		config: config,
	}
}

// Start starts the HTTP server
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server != nil {
		return fmt.Errorf("%s server already running", s.config.Name)
	}

	// Create listener
	lc := net.ListenConfig{}

	listener, err := lc.Listen(ctx, "tcp", s.config.Address)
	if err != nil {
		return fmt.Errorf(
			"failed to create %s listener on %s: %w",
			s.config.Name,
			s.config.Address,
			err,
		)
	}

	// Wrap with TLS if configured
	logger := s.config.Logger
	if logger == nil {
		logger = log.WithField("component", "http-server")
	}

	if s.config.TLSConfig != nil {
		logger.WithFields(log.Fields{
			"server":  s.config.Name,
			"address": listener.Addr().String(),
		}).Info("Starting HTTPS server")
		listener = tls.NewListener(listener, s.config.TLSConfig)
	} else {
		logger.WithFields(log.Fields{
			"server":  s.config.Name,
			"address": listener.Addr().String(),
		}).Info("Starting HTTP server")
	}

	s.listener = listener
	s.server = &http.Server{
		Handler:           s.config.Handler,
		ReadHeaderTimeout: s.config.ReadHeaderTimeout,
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})

	// Start server in goroutine
	go func() {
		defer close(s.done)

		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.WithFields(log.Fields{
				"server": s.config.Name,
				"error":  err,
			}).Error("HTTP server error")
		}
	}()

	return nil
}

// Stop stops the HTTP server gracefully
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server == nil {
		return nil
	}

	logger := s.config.Logger
	if logger == nil {
		logger = log.WithField("component", "http-server")
	}

	logger.WithField("server", s.config.Name).Info("Stopping HTTP server")

	// Cancel context
	if s.cancel != nil {
		s.cancel()
	}

	// Shutdown server gracefully
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		logger.WithFields(log.Fields{
			"server": s.config.Name,
			"error":  err,
		}).Warn("Failed to shutdown HTTP server gracefully, forcing close")

		// Force close listener
		if s.listener != nil {
			s.listener.Close()
		}
	}

	// Wait for server goroutine to exit
	if s.done != nil {
		<-s.done
	}

	// Clean up
	s.server = nil
	s.listener = nil
	s.ctx = nil
	s.cancel = nil
	s.done = nil

	logger.WithField("server", s.config.Name).Info("HTTP server stopped")

	return nil
}
