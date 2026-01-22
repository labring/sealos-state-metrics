package pod

// Config contains configuration for the Pod collector
type Config struct {
	Enabled          bool     `yaml:"enabled"          env:"ENABLED"`
	Namespaces       []string `yaml:"namespaces"       env:"NAMESPACES"        envSeparator:","`
	RestartThreshold int      `yaml:"restartThreshold" env:"RESTART_THRESHOLD"`
}

// NewDefaultConfig returns the default configuration for Pod collector
// This function only returns hard-coded defaults without any env parsing
func NewDefaultConfig() *Config {
	return &Config{
		Enabled:          true,
		Namespaces:       []string{},
		RestartThreshold: 5,
	}
}
