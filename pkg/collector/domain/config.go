package domain

import (
	"time"
)

// Config contains configuration for the Domain collector
type Config struct {
	Domains          []string      `yaml:"domains"          env:"DOMAINS"            envSeparator:","`
	CheckTimeout     time.Duration `yaml:"checkTimeout"     env:"CHECK_TIMEOUT"`
	CheckInterval    time.Duration `yaml:"checkInterval"    env:"CHECK_INTERVAL"`
	IncludeCertCheck bool          `yaml:"includeCertCheck" env:"INCLUDE_CERT_CHECK"`
	IncludeHTTPCheck bool          `yaml:"includeHTTPCheck" env:"INCLUDE_HTTP_CHECK"`
}

// NewDefaultConfig returns the default configuration for Domain collector
// This function only returns hard-coded defaults without any env parsing
func NewDefaultConfig() *Config {
	return &Config{
		Domains:          []string{},
		CheckTimeout:     5 * time.Second,
		CheckInterval:    5 * time.Minute,
		IncludeCertCheck: true,
		IncludeHTTPCheck: true,
	}
}
