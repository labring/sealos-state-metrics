package config

import (
	"errors"
	"fmt"

	"github.com/labring/sealos-state-metrics/pkg/collector"
)

// WrapConfigLoader wraps multiple config loaders in a chain
// Loaders are executed in the order they are added
// Later loaders override values from earlier loaders
type WrapConfigLoader struct {
	loaders []collector.ConfigLoader
}

// NewWrapConfigLoader creates a new wrap config loader
func NewWrapConfigLoader() *WrapConfigLoader {
	return &WrapConfigLoader{
		loaders: make([]collector.ConfigLoader, 0),
	}
}

// Add adds a config loader to the chain
// Loaders added later have higher priority
func (w *WrapConfigLoader) Add(loader collector.ConfigLoader) *WrapConfigLoader {
	w.loaders = append(w.loaders, loader)
	return w
}

// LoadModuleConfig loads configuration from all loaders in the chain
// Each loader can override values from previous loaders
func (w *WrapConfigLoader) LoadModuleConfig(moduleKey string, target any) error {
	var errs []error

	for _, loader := range w.loaders {
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

// Loaders returns all loaders in the chain
func (w *WrapConfigLoader) Loaders() []collector.ConfigLoader {
	return w.loaders
}

// Len returns the number of loaders in the chain
func (w *WrapConfigLoader) Len() int {
	return len(w.loaders)
}
