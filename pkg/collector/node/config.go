package node

import (
	"time"
)

// Config contains configuration for the Node collector
type Config struct {
	IgnoreNewNodeDuration time.Duration `yaml:"ignoreNewNodeDuration" env:"IGNORE_NEW_NODE_DURATION"`
}

// NewDefaultConfig returns the default configuration for Node collector
// This function only returns hard-coded defaults without any env parsing
func NewDefaultConfig() *Config {
	return &Config{
		IgnoreNewNodeDuration: 30 * time.Minute,
	}
}
