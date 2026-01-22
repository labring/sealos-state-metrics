package zombie

import (
	"time"
)

// Config contains configuration for the Zombie collector
type Config struct {
	Enabled       bool          `yaml:"enabled"       env:"ENABLED"`
	CheckInterval time.Duration `yaml:"checkInterval" env:"CHECK_INTERVAL"`
}

// NewDefaultConfig returns the default configuration for Zombie collector
// This function only returns hard-coded defaults without any env parsing
func NewDefaultConfig() *Config {
	return &Config{
		Enabled:       true,
		CheckInterval: 30 * time.Second,
	}
}
