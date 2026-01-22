package imagepull

import (
	"time"
)

// Config contains configuration for the ImagePull collector
type Config struct {
	SlowPullThreshold time.Duration `yaml:"slowPullThreshold" env:"SLOW_PULL_THRESHOLD"`
	EventRetention    time.Duration `yaml:"eventRetention"    env:"EVENT_RETENTION"`
}

// NewDefaultConfig returns the default configuration for ImagePull collector
// This function only returns hard-coded defaults without any env parsing
func NewDefaultConfig() *Config {
	return &Config{
		SlowPullThreshold: 5 * time.Minute,
		EventRetention:    1 * time.Hour,
	}
}
