package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"
	"github.com/zijiren233/sealos-state-metric/app"
	_ "github.com/zijiren233/sealos-state-metric/pkg/collector/all" // Import all collectors
	"github.com/zijiren233/sealos-state-metric/pkg/config"
	"github.com/zijiren233/sealos-state-metric/pkg/logger"
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
	server := app.NewServer(cfg, configContent)

	// Setup config reloader if config path is provided
	var reloader *config.Reloader
	if cfg.ConfigPath != "" {
		reloader, err = config.NewReloader(cfg.ConfigPath, func(newConfigContent []byte) error {
			return handleReload(cliArgs, newConfigContent, server)
		})
		if err != nil {
			log.WithError(err).Fatal("Failed to create config reloader")
		}
	}

	// Setup signal handling with cancellable context
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start config reloader
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

	// Run server (blocks until context is cancelled or error)
	if err := server.Run(ctx); err != nil {
		log.WithError(err).Fatal("Server exited with error")
	}

	log.Info("Server exited successfully")
}

// handleReload handles configuration reload for both logger and server
func handleReload(cliArgs []string, newConfigContent []byte, server *app.Server) error {
	// Load new configuration
	newConfig, err := config.LoadGlobalConfig(config.LoadOptions{
		Args:          cliArgs,
		ConfigContent: newConfigContent,
	})
	if err != nil {
		return err
	}

	// Validate new configuration
	if err := newConfig.Validate(); err != nil {
		return err
	}

	// Reload logger first
	logger.InitLog(
		logger.WithDebug(newConfig.Logging.Debug),
		logger.WithLevel(newConfig.Logging.Level),
		logger.WithFormat(newConfig.Logging.Format),
	)

	log.Info("Logger reloaded")

	// Reload server
	return server.Reload(newConfigContent, newConfig)
}
