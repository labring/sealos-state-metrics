package config

import (
	"errors"
	"fmt"

	"github.com/labring/sealos-state-metrics/pkg/collector"
)

// CompositeConfigLoader implements pipe mode - multiple loaders in sequence
// Each loader can override values from previous loaders
// This enables configuration priority: defaults -> file -> env
type CompositeConfigLoader struct {
	loaders []collector.ConfigLoader
}

// NewCompositeConfigLoader creates a new composite config loader
// Usage example:
//
//	loader := NewCompositeConfigLoader(
//	    NewModuleConfigLoader("config.yaml"),  // File config (low priority)
//	    NewEnvConfigLoader("APP_"),            // Env vars (high priority)
//	)
func NewCompositeConfigLoader(loaders ...collector.ConfigLoader) *CompositeConfigLoader {
	return &CompositeConfigLoader{
		loaders: loaders,
	}
}

// LoadModuleConfig loads configuration from all loaders in sequence
// Later loaders override values from earlier loaders
func (c *CompositeConfigLoader) LoadModuleConfig(moduleKey string, target any) error {
	var errs []error

	for _, loader := range c.loaders {
		if err := loader.LoadModuleConfig(moduleKey, target); err != nil {
			// Continue with next loader even if one fails
			errs = append(errs, err)
			continue
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to load module config %q: %w", moduleKey, errors.Join(errs...))
	}

	return nil
}

// AddLoader adds a loader to the pipe
func (c *CompositeConfigLoader) AddLoader(loader collector.ConfigLoader) {
	c.loaders = append(c.loaders, loader)
}

// Loaders returns all loaders in the pipe
func (c *CompositeConfigLoader) Loaders() []collector.ConfigLoader {
	return c.loaders
}
