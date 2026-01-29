package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/labring/sealos-state-metrics/pkg/collector/all" // Import all collectors
	"github.com/labring/sealos-state-metrics/pkg/config"
	"github.com/labring/sealos-state-metrics/pkg/logger"
	"github.com/labring/sealos-state-metrics/pkg/pprof"
	"github.com/labring/sealos-state-metrics/server"
	log "github.com/sirupsen/logrus"
)

func main() {
	// Store CLI args for config reload (skip program name)
	cliArgs := os.Args[1:]

	// Load configuration: CLI args (defaults) → YAML → env vars
	cfg, err := config.LoadGlobalConfig(config.LoadOptions{
		Args: cliArgs,
	})
	if err != nil {
		log.WithError(err).Fatal("Failed to load configuration")
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		log.WithError(err).Fatal("Configuration validation failed")
	}

	// Initialize logger
	logger.InitLog(
		logger.WithDebug(cfg.Logging.Debug),
		logger.WithLevel(cfg.Logging.Level),
		logger.WithFormat(cfg.Logging.Format),
	)

	log.WithFields(log.Fields{
		"collectors":     cfg.EnabledCollectors,
		"leaderElection": cfg.LeaderElection.Enabled,
		"metricsAddress": cfg.Server.Address,
	}).Info("Configuration loaded")

	// Read config file content if provided
	var configContent []byte
	if cfg.ConfigPath != "" {
		configContent, err = os.ReadFile(cfg.ConfigPath)
		if err != nil {
			log.WithError(err).Fatal("Failed to read config file")
		}
	}

	// Create server
	srv := server.New(cfg, configContent)

	// Create pprof server
	var pprofServer *pprof.Server
	if cfg.Pprof.Enabled {
		pprofServer = pprof.NewServer(cfg.Pprof.Port)
	}

	// Setup config reloader if config path is provided
	var reloader *config.Reloader
	if cfg.ConfigPath != "" {
		reloader, err = config.NewReloader(cfg.ConfigPath, func(newConfigContent []byte) error {
			return handleReload(cliArgs, newConfigContent, srv, pprofServer)
		})
		if err != nil {
			log.WithError(err).Fatal("Failed to create config reloader")
		}
	}

	// Setup signal handling with cancellable context
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Initialize server first (this may take several seconds)
	if err := srv.Init(ctx); err != nil {
		log.WithError(err).Fatal("Failed to initialize server")
	}

	// Start pprof server if enabled
	if pprofServer != nil {
		if err := pprofServer.Start(ctx); err != nil {
			log.WithError(err).Fatal("Failed to start pprof server")
		}
		defer func() {
			if err := pprofServer.Stop(); err != nil {
				log.WithError(err).Error("Failed to stop pprof server")
			}
		}()
	}

	// Start config reloader AFTER server is fully initialized
	if reloader != nil {
		if err := reloader.Start(ctx); err != nil {
			log.WithError(err).Fatal("Failed to start config reloader")
		}
		defer func() {
			if err := reloader.Stop(); err != nil {
				log.WithError(err).Error("Failed to stop config reloader")
			}
		}()

		log.WithField("config_path", cfg.ConfigPath).Info("Configuration hot reload enabled")
	}

	// Start HTTP server and wait (blocks until context is cancelled or error)
	if err := srv.Serve(); err != nil {
		log.WithError(err).Fatal("Server exited with error")
	}

	log.Info("Server exited successfully")
}

// handleReload handles configuration reload for logger, server and pprof
func handleReload(
	cliArgs []string,
	newConfigContent []byte,
	srv *server.Server,
	pprofServer *pprof.Server,
) error {
	// Load and validate new configuration
	newConfig, err := loadAndValidateConfig(cliArgs, newConfigContent)
	if err != nil {
		return err
	}

	// Reload logger
	reloadLogger(newConfig)

	// Reload pprof server
	reloadPprofServer(pprofServer, newConfig)

	// Reload main server
	return srv.Reload(newConfigContent, newConfig)
}

// loadAndValidateConfig loads and validates configuration
func loadAndValidateConfig(cliArgs []string, configContent []byte) (*config.GlobalConfig, error) {
	newConfig, err := config.LoadGlobalConfig(config.LoadOptions{
		Args:          cliArgs,
		ConfigContent: configContent,
		DisableExit:   true,
	})
	if err != nil {
		return nil, err
	}

	if err := newConfig.Validate(); err != nil {
		return nil, err
	}

	return newConfig, nil
}

// reloadLogger reloads logger configuration
func reloadLogger(cfg *config.GlobalConfig) {
	logger.InitLog(
		logger.WithDebug(cfg.Logging.Debug),
		logger.WithLevel(cfg.Logging.Level),
		logger.WithFormat(cfg.Logging.Format),
	)

	log.Info("Logger reloaded")
}

// reloadPprofServer reloads pprof server based on new configuration
func reloadPprofServer(pprofServer *pprof.Server, cfg *config.GlobalConfig) {
	if pprofServer != nil {
		// Pprof server is running
		if !cfg.Pprof.Enabled {
			// Pprof disabled, stop it
			if err := pprofServer.Stop(); err != nil {
				log.WithError(err).Error("Failed to stop pprof server")
			}

			log.Info("Pprof server stopped")

			return
		}

		// Pprof still enabled, reload with new config
		if err := pprofServer.Reload(context.Background(), cfg.Pprof.Port); err != nil {
			log.WithError(err).Error("Failed to reload pprof server")
		} else {
			log.Info("Pprof server reloaded")
		}

		return
	}

	// Pprof was not running
	if cfg.Pprof.Enabled {
		// Start new pprof server
		// Note: This starts a detached server since we can't update the pointer
		// The server will continue running until the process exits
		newServer := pprof.NewServer(cfg.Pprof.Port)
		if err := newServer.Start(context.Background()); err != nil {
			log.WithError(err).Error("Failed to start pprof server after reload")
		} else {
			log.Info("Pprof server started after reload")
		}
	}
}
