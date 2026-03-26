//nolint:testpackage // Tests require access to internal types
package config

import (
	"errors"
	"strings"
	"testing"
)

type stubLoader struct {
	err  error
	load func(moduleKey string, target any) error
}

func (s stubLoader) LoadModuleConfig(moduleKey string, target any) error {
	if s.load != nil {
		return s.load(moduleKey, target)
	}

	return s.err
}

type chainTestConfig struct {
	Name string
}

func TestCompositeConfigLoader_ReturnsErrorAndContinues(t *testing.T) {
	cfg := &chainTestConfig{}

	loader := NewCompositeConfigLoader(
		stubLoader{err: errors.New("loader one failed")},
		stubLoader{
			load: func(_ string, target any) error {
				target.(*chainTestConfig).Name = "loaded"
				return nil
			},
		},
	)

	err := loader.LoadModuleConfig("collectors.test", cfg)
	if err == nil {
		t.Fatal("expected aggregated error, got nil")
	}

	if cfg.Name != "loaded" {
		t.Fatalf("expected later loader to still run, got name %q", cfg.Name)
	}

	if !strings.Contains(err.Error(), "collectors.test") {
		t.Fatalf("expected module key in error, got %v", err)
	}

	if !strings.Contains(err.Error(), "loader one failed") {
		t.Fatalf("expected original loader error, got %v", err)
	}
}

func TestWrapConfigLoader_ReturnsErrorAndContinues(t *testing.T) {
	cfg := &chainTestConfig{}

	loader := NewWrapConfigLoader().
		Add(stubLoader{err: errors.New("loader one failed")}).
		Add(stubLoader{
			load: func(_ string, target any) error {
				target.(*chainTestConfig).Name = "loaded"
				return nil
			},
		})

	err := loader.LoadModuleConfig("collectors.test", cfg)
	if err == nil {
		t.Fatal("expected aggregated error, got nil")
	}

	if cfg.Name != "loaded" {
		t.Fatalf("expected later loader to still run, got name %q", cfg.Name)
	}

	if !strings.Contains(err.Error(), "collectors.test") {
		t.Fatalf("expected module key in error, got %v", err)
	}

	if !strings.Contains(err.Error(), "loader one failed") {
		t.Fatalf("expected original loader error, got %v", err)
	}
}
