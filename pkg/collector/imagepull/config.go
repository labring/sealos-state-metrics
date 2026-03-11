package imagepull

import (
	"time"
)

// Config contains configuration for the ImagePull collector
type Config struct {
	SlowPullThreshold time.Duration `yaml:"slowPullThreshold" env:"SLOW_PULL_THRESHOLD"`
}

// NewDefaultConfig returns the default configuration for ImagePull collector
// This function only returns hard-coded defaults without any env parsing
func NewDefaultConfig() *Config {
	return &Config{
		SlowPullThreshold: 5 * time.Minute,
	}
}
