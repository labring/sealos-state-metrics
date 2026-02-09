package database

import "time"

// Config contains configuration for the Database collector
type Config struct {
	CheckInterval time.Duration `yaml:"checkInterval" json:"check_interval" env:"CHECK_INTERVAL"`
	// Timeout for database connection attempts
	CheckTimeout time.Duration `yaml:"checkTimeout" json:"check_timeout" env:"CHECK_TIMEOUT"`
	// List of namespaces to scan. Empty means all namespaces
	Namespaces []string `yaml:"namespaces" json:"namespaces" env:"NAMESPACES" envSeparator:","`
}

// NewDefaultConfig returns the default configuration
func NewDefaultConfig() *Config {
	return &Config{
		CheckInterval: 5 * time.Minute,
		CheckTimeout:  10 * time.Second,
		Namespaces:    []string{}, // Empty means all namespaces
	}
}
