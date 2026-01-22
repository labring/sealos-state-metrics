// Package config provides configuration loading and management for the application
package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/caarlos0/env/v9"
	"github.com/joho/godotenv"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// GlobalConfig contains only top-level application configuration
// Module-specific configs are managed by each module independently
type GlobalConfig struct {
	// Configuration files
	ConfigFile string `yaml:"-" short:"c" help:"Path to configuration file (YAML format)" type:"path"`
	EnvFile    string `yaml:"-" help:"Path to .env file for environment variables" default:".env" type:"path"`

	// Server configuration
	Server ServerConfig `yaml:"server" embed:"" prefix:"server-" envprefix:"SERVER_"`

	// Kubernetes client configuration
	Kubernetes KubernetesConfig `yaml:"kubernetes" embed:"" prefix:"" envprefix:"KUBERNETES_"`

	// Metrics configuration
	Metrics MetricsConfig `yaml:"metrics" embed:"" prefix:"metrics-" envprefix:"METRICS_"`

	// Leader election configuration
	LeaderElection LeaderElectionConfig `yaml:"leaderElection" embed:"" prefix:"leader-election-" envprefix:"LEADER_ELECTION_"`

	// Logging configuration
	Logging LoggingConfig `yaml:"logging" embed:"" prefix:"log-" envprefix:"LOGGING_"`

	// Performance tuning
	Performance PerformanceConfig `yaml:"performance" embed:"" prefix:"" envprefix:"PERFORMANCE_"`

	// Enabled collectors (list of collector names)
	EnabledCollectors []string `yaml:"enabledCollectors" help:"Comma-separated list of enabled collectors" default:"domain,node,pod,imagepull,zombie" env:"ENABLED_COLLECTORS" sep:","`
}

// ServerConfig contains HTTP server configuration
type ServerConfig struct {
	Address     string `yaml:"address"     name:"address"      env:"ADDRESS"      default:":9090"     help:"Server listen address"`
	MetricsPath string `yaml:"metricsPath" name:"metrics-path" env:"METRICS_PATH" default:"/metrics"  help:"Metrics endpoint path"`
	HealthPath  string `yaml:"healthPath"  name:"health-path"  env:"HEALTH_PATH"  default:"/health"   help:"Health check endpoint path"`
}

// KubernetesConfig contains Kubernetes client configuration
type KubernetesConfig struct {
	Kubeconfig string  `yaml:"kubeconfig" name:"kubeconfig" env:"KUBECONFIG"  help:"Path to kubeconfig file (leave empty for in-cluster config)" type:"path"`
	QPS        float32 `yaml:"qps"        name:"qps"        env:"QPS"         default:"50"    help:"Kubernetes client QPS limit"`
	Burst      int     `yaml:"burst"      name:"burst"      env:"BURST"       default:"100"   help:"Kubernetes client burst limit"`
}

// MetricsConfig contains Prometheus metrics configuration
type MetricsConfig struct {
	Namespace string `yaml:"namespace" name:"namespace" env:"NAMESPACE" default:"sealos" help:"Prometheus metrics namespace"`
}

// LeaderElectionConfig contains leader election configuration
type LeaderElectionConfig struct {
	Enabled       bool          `yaml:"enabled"       name:"enabled"        env:"ENABLED"        default:"true"                  help:"Enable leader election"`
	Namespace     string        `yaml:"namespace"     name:"namespace"      env:"NAMESPACE"                                      help:"Namespace for leader election lease (empty disables LE)"`
	LeaseName     string        `yaml:"leaseName"     name:"lease-name"     env:"LEASE_NAME"     default:"sealos-state-metric"   help:"Name of the leader election lease"`
	LeaseDuration time.Duration `yaml:"leaseDuration" name:"lease-duration" env:"LEASE_DURATION" default:"15s"                    help:"Leader election lease duration"`
	RenewDeadline time.Duration `yaml:"renewDeadline" name:"renew-deadline" env:"RENEW_DEADLINE" default:"10s"                    help:"Leader election renew deadline"`
	RetryPeriod   time.Duration `yaml:"retryPeriod"   name:"retry-period"   env:"RETRY_PERIOD"   default:"2s"                     help:"Leader election retry period"`
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Level  string `yaml:"level"  name:"level"  env:"LEVEL"  default:"info" enum:"debug,info,warn,error" help:"Log level"`
	Format string `yaml:"format" name:"format" env:"FORMAT" default:"json" enum:"json,text"             help:"Log format"`
	Debug  bool   `yaml:"debug"  name:"debug"  env:"DEBUG"  default:"false"                            help:"Enable debug mode"`
}

// ToLoggerOptions converts LoggingConfig to logger initialization options
func (c *LoggingConfig) ToLoggerOptions() (debug bool, level, format string) {
	return c.Debug, c.Level, c.Format
}

// PerformanceConfig contains performance tuning configuration
type PerformanceConfig struct {
	InformerResyncPeriod  time.Duration `yaml:"informerResyncPeriod"  name:"informer-resync-period"   env:"INFORMER_RESYNC_PERIOD"  default:"10m" help:"Kubernetes informer resync period" hidden:""`
	Workers               int           `yaml:"workers"               name:"workers"                  env:"WORKERS"                 default:"10"  help:"Number of worker goroutines"       hidden:""`
	MetricsUpdateInterval time.Duration `yaml:"metricsUpdateInterval" name:"metrics-update-interval"  env:"METRICS_UPDATE_INTERVAL" default:"1m"  help:"Metrics update interval"            hidden:""`
}

// LoadEnvFile loads environment variables from a .env file
func LoadEnvFile(path string) error {
	return godotenv.Load(path)
}

// LoadFromYAML loads configuration from YAML file into cfg
func LoadFromYAML(path string, cfg *GlobalConfig) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	log.WithField("file", path).Info("Configuration loaded from YAML")

	return nil
}

// ParseEnv parses environment variables into cfg
func ParseEnv(cfg *GlobalConfig) error {
	return env.Parse(cfg)
}

// ConfigLoader loads configuration from multiple sources with proper precedence
type ConfigLoader struct {
	configFile string
	envFile    string
}

// NewConfigLoader creates a new configuration loader
func NewConfigLoader(configFile, envFile string) *ConfigLoader {
	return &ConfigLoader{
		configFile: configFile,
		envFile:    envFile,
	}
}

// Load loads configuration with precedence: CLI flags > ENV vars > Config file > Defaults
func (l *ConfigLoader) Load() (*GlobalConfig, error) {
	// Step 1: Load .env file if specified (lowest priority, before system env)
	if l.envFile != "" {
		if err := godotenv.Load(l.envFile); err != nil {
			log.WithFields(log.Fields{
				"file":  l.envFile,
				"error": err,
			}).Debug("No .env file loaded")
		} else {
			log.WithField("file", l.envFile).Info("Loaded environment from .env file")
		}
	}

	// Step 2: Start with defaults (built into struct tags via envDefault)
	cfg := &GlobalConfig{
		EnabledCollectors: []string{"domain", "node", "pod", "imagepull", "zombie"},
	}

	// Step 3: Load from YAML config file if specified
	if l.configFile != "" {
		if err := l.loadFromYAML(cfg); err != nil {
			return nil, fmt.Errorf("failed to load config from YAML: %w", err)
		}
	}

	// Step 4: Override with environment variables (highest priority)
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("failed to parse environment variables: %w", err)
	}

	log.WithFields(log.Fields{
		"server":         cfg.Server.Address,
		"collectors":     cfg.EnabledCollectors,
		"leaderElection": cfg.LeaderElection.Enabled,
	}).Info("Configuration loaded")

	return cfg, nil
}

// loadFromYAML loads configuration from YAML file
func (l *ConfigLoader) loadFromYAML(cfg *GlobalConfig) error {
	data, err := os.ReadFile(l.configFile)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	log.WithField("file", l.configFile).Info("Configuration loaded from YAML")

	return nil
}

// Validate validates the global configuration
func (c *GlobalConfig) Validate() error {
	if c.Server.Address == "" {
		return errors.New("server.address cannot be empty")
	}

	if c.Metrics.Namespace == "" {
		return errors.New("metrics.namespace cannot be empty")
	}

	// Auto-disable leader election if namespace is empty
	if c.LeaderElection.Namespace == "" {
		if c.LeaderElection.Enabled {
			log.Warn("Leader election namespace is empty, automatically disabling leader election")
			c.LeaderElection.Enabled = false
		}
	} else if c.LeaderElection.Enabled {
		// Only validate timing constraints if leader election is enabled
		if c.LeaderElection.RenewDeadline >= c.LeaderElection.LeaseDuration {
			return errors.New("leaderElection.renewDeadline must be less than leaseDuration")
		}
	}

	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("invalid logging.level: %s", c.Logging.Level)
	}

	return nil
}
