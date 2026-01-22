package main

import (
	"context"
	"os"

	"github.com/alecthomas/kong"
	log "github.com/sirupsen/logrus"
	"github.com/zijiren233/sealos-state-metric/app"
	_ "github.com/zijiren233/sealos-state-metric/pkg/collector/all" // Import all collectors
	"github.com/zijiren233/sealos-state-metric/pkg/config"
	"github.com/zijiren233/sealos-state-metric/pkg/logger"
)

func main() {
	// Parse command-line flags with kong
	cfg := &config.GlobalConfig{}
	ctx := kong.Parse(cfg,
		kong.Name("sealos-state-metric"),
		kong.Description("Sealos state metrics collector for Kubernetes"),
		kong.UsageOnError(),
	)

	// Load from .env file if specified
	if cfg.EnvFile != "" {
		if err := config.LoadEnvFile(cfg.EnvFile); err != nil {
			log.WithError(err).Debug("No .env file loaded")
		} else {
			log.WithField("file", cfg.EnvFile).Info("Loaded environment from .env file")
		}
	}

	// Load configuration from YAML if specified
	if cfg.ConfigFile != "" {
		if err := config.LoadFromYAML(cfg.ConfigFile, cfg); err != nil {
			ctx.Fatalf("Failed to load config from YAML: %v", err)
		}
	}

	// Parse environment variables (will fill in any missing values)
	if err := config.ParseEnv(cfg); err != nil {
		ctx.Fatalf("Failed to parse environment variables: %v", err)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		ctx.Fatalf("Configuration validation failed: %v", err)
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

	// Create and run server
	appCtx := context.Background()

	server, err := app.NewServer(cfg, cfg.ConfigFile)
	if err != nil {
		log.WithError(err).Error("Failed to create server")
		os.Exit(1)
	}

	if err := server.Run(appCtx); err != nil {
		log.WithError(err).Error("Server exited with error")
		os.Exit(1)
	}

	log.Info("Server exited successfully")
}
