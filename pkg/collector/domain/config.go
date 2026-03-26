package domain

import (
	"time"
)

// Config contains configuration for the Domain collector
type Config struct {
	Domains          []any         `yaml:"domains"          env:"-"`
	DomainsEnv       []string      `yaml:"-"                env:"DOMAINS"            envSeparator:","`
	CheckTimeout     time.Duration `yaml:"checkTimeout"     env:"CHECK_TIMEOUT"`
	CheckInterval    time.Duration `yaml:"checkInterval"    env:"CHECK_INTERVAL"`
	IncludeCertCheck bool          `yaml:"includeCertCheck" env:"INCLUDE_CERT_CHECK"`
	IncludeHTTPCheck bool          `yaml:"includeHTTPCheck" env:"INCLUDE_HTTP_CHECK"`
	IncludeIPv4      bool          `yaml:"includeIPv4"      env:"INCLUDE_IPV4"`
	IncludeIPv6      bool          `yaml:"includeIPv6"      env:"INCLUDE_IPV6"`
}

// NewDefaultConfig returns the default configuration for Domain collector
// This function only returns hard-coded defaults without any env parsing
func NewDefaultConfig() *Config {
	return &Config{
		Domains:          []any{},
		DomainsEnv:       []string{},
		CheckTimeout:     15 * time.Second,
		CheckInterval:    1 * time.Minute,
		IncludeCertCheck: true,
		IncludeHTTPCheck: true,
		IncludeIPv4:      true,
		IncludeIPv6:      true,
	}
}
