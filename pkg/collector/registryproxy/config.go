package registryproxy

import "time"

// Config contains configuration for the RegistryProxy collector.
type Config struct {
	Registries    []any         `yaml:"registries"    env:"-"`
	RegistriesEnv []string      `yaml:"-"             env:"REGISTRIES"     envSeparator:","` //nolint:lll
	CheckTimeout  time.Duration `yaml:"checkTimeout"  env:"CHECK_TIMEOUT"`
	CheckInterval time.Duration `yaml:"checkInterval" env:"CHECK_INTERVAL"`
}

// NewDefaultConfig returns the default configuration for RegistryProxy collector.
func NewDefaultConfig() *Config {
	return &Config{
		Registries:    []any{},
		RegistriesEnv: []string{},
		CheckTimeout:  30 * time.Second,
		CheckInterval: 1 * time.Minute,
	}
}
