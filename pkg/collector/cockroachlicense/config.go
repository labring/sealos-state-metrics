package cockroachlicense

import "time"

// ClusterConfig identifies one CockroachDB cluster to query.
type ClusterConfig struct {
	Name string `yaml:"name" json:"name"`
	DSN  string `yaml:"dsn"  json:"dsn"`
}

// Config contains configuration for the Cockroach license collector.
type Config struct {
	CheckInterval time.Duration   `yaml:"checkInterval" json:"check_interval" env:"CHECK_INTERVAL"`
	CheckTimeout  time.Duration   `yaml:"checkTimeout"  json:"check_timeout"  env:"CHECK_TIMEOUT"`
	Clusters      []ClusterConfig `yaml:"clusters"      json:"clusters"       env:"-"`
}

// NewDefaultConfig returns the default configuration.
func NewDefaultConfig() *Config {
	return &Config{
		CheckInterval: 5 * time.Minute,
		CheckTimeout:  10 * time.Second,
		Clusters:      []ClusterConfig{},
	}
}
