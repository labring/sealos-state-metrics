//nolint:testpackage // Tests require access to internal functions
package config

import (
	"reflect"
	"testing"
	"time"

	"github.com/mitchellh/mapstructure"
)

// TestConfig is a test configuration struct
type TestConfig struct {
	Enabled       bool          `mapstructure:"enabled"        yaml:"enabled"`
	Count         int           `mapstructure:"count"          yaml:"count"`
	Timeout       time.Duration `mapstructure:"timeout"        yaml:"timeout"`
	Name          string        `mapstructure:"name"           yaml:"name"`
	Tags          []string      `mapstructure:"tags"           yaml:"tags"`
	NestedConfig  NestedConfig  `mapstructure:"nested"         yaml:"nested"`
	OptionalField string        `mapstructure:"optional_field" yaml:"optional_field"`
}

type NestedConfig struct {
	Value string `mapstructure:"value" yaml:"value"`
	Port  int    `mapstructure:"port"  yaml:"port"`
}

type DomainModuleTestConfig struct {
	Domains      []any         `yaml:"domains"`
	DomainsEnv   []string      `yaml:"-"            env:"DOMAINS"      envSeparator:","`
	CheckTimeout time.Duration `yaml:"checkTimeout"`
	IncludeIPv4  bool          `yaml:"includeIPv4"  env:"INCLUDE_IPV4"`
	IncludeIPv6  bool          `yaml:"includeIPv6"  env:"INCLUDE_IPV6"`
}

func TestModuleConfigLoader_BasicLoad(t *testing.T) {
	// Create temporary YAML config file
	yamlContent := `
collectors:
  node:
    enabled: true
    count: 42
    name: test-collector
    tags:
      - tag1
      - tag2
    nested:
      value: nested-value
      port: 8080
`

	tmpFile := createTempYAML(t, yamlContent)

	// Create loader
	loader := NewModuleConfigLoader(tmpFile)

	// Load config
	var config TestConfig

	err := loader.LoadModuleConfig("collectors.node", &config)
	if err != nil {
		t.Fatalf("LoadModuleConfig failed: %v", err)
	}

	// Verify results
	if !config.Enabled {
		t.Errorf("Expected Enabled=true, got %v", config.Enabled)
	}

	if config.Count != 42 {
		t.Errorf("Expected Count=42, got %d", config.Count)
	}

	if config.Name != "test-collector" {
		t.Errorf("Expected Name=test-collector, got %s", config.Name)
	}

	if len(config.Tags) != 2 || config.Tags[0] != "tag1" || config.Tags[1] != "tag2" {
		t.Errorf("Expected Tags=[tag1, tag2], got %v", config.Tags)
	}

	if config.NestedConfig.Value != "nested-value" {
		t.Errorf("Expected NestedConfig.Value=nested-value, got %s", config.NestedConfig.Value)
	}

	if config.NestedConfig.Port != 8080 {
		t.Errorf("Expected NestedConfig.Port=8080, got %d", config.NestedConfig.Port)
	}
}

func TestModuleConfigLoader_WithYAMLTagName(t *testing.T) {
	yamlContent := `
collectors:
  pod:
    enabled: false
    count: 100
    name: pod-collector
`

	tmpFile := createTempYAML(t, yamlContent)

	// Create loader with yaml tag name
	loader := NewModuleConfigLoader(tmpFile, WithModuleTagName("yaml"))

	var config TestConfig

	err := loader.LoadModuleConfig("collectors.pod", &config)
	if err != nil {
		t.Fatalf("LoadModuleConfig failed: %v", err)
	}

	if config.Enabled {
		t.Errorf("Expected Enabled=false, got %v", config.Enabled)
	}

	if config.Count != 100 {
		t.Errorf("Expected Count=100, got %d", config.Count)
	}

	if config.Name != "pod-collector" {
		t.Errorf("Expected Name=pod-collector, got %s", config.Name)
	}
}

func TestModuleConfigLoader_WithCustomDecodeHook(t *testing.T) {
	yamlContent := `
collectors:
  cert:
    enabled: true
    count: 50
    timeout: "10s"
    name: cert-collector
`

	tmpFile := createTempYAML(t, yamlContent)

	// Test with custom decode hook for time.Duration
	loader := NewModuleConfigLoader(tmpFile, WithModuleDecodeHook(
		mapstructure.StringToTimeDurationHookFunc(),
	))

	var config TestConfig

	err := loader.LoadModuleConfig("collectors.cert", &config)
	if err != nil {
		t.Fatalf("LoadModuleConfig failed: %v", err)
	}

	if !config.Enabled {
		t.Errorf("Expected Enabled=true, got %v", config.Enabled)
	}

	if config.Count != 50 {
		t.Errorf("Expected Count=50, got %d", config.Count)
	}

	if config.Name != "cert-collector" {
		t.Errorf("Expected Name=cert-collector, got %s", config.Name)
	}
	// Custom decode hook should convert "10s" to time.Duration
	if config.Timeout != 10*time.Second {
		t.Errorf("Expected Timeout=10s (with custom hook), got %v", config.Timeout)
	}
}

func TestModuleConfigLoader_WithoutDecodeHook(t *testing.T) {
	yamlContent := `
collectors:
  domain:
    enabled: true
    count: 100
    name: domain-collector
`

	tmpFile := createTempYAML(t, yamlContent)

	// Test WITHOUT decode hook - only basic types work
	loader := NewModuleConfigLoader(tmpFile)

	var config TestConfig

	err := loader.LoadModuleConfig("collectors.domain", &config)
	if err != nil {
		t.Fatalf("LoadModuleConfig failed: %v", err)
	}

	// timeout should remain zero (not in YAML)
	if config.Timeout != 0 {
		t.Errorf("Expected Timeout=0 (not in YAML), got %v", config.Timeout)
	}

	if config.Count != 100 {
		t.Errorf("Expected Count=100, got %d", config.Count)
	}

	if !config.Enabled {
		t.Errorf("Expected Enabled=true, got %v", config.Enabled)
	}
}

func TestModuleConfigLoader_WeaklyTypedInput(t *testing.T) {
	yamlContent := `
collectors:
  domain:
    enabled: "true"
    count: "123"
    name: 456
`

	tmpFile := createTempYAML(t, yamlContent)

	// WeaklyTypedInput is enabled by default
	loader := NewModuleConfigLoader(tmpFile)

	var config TestConfig

	err := loader.LoadModuleConfig("collectors.domain", &config)
	if err != nil {
		t.Fatalf("LoadModuleConfig failed: %v", err)
	}

	// String "true" should convert to bool true
	if !config.Enabled {
		t.Errorf("Expected Enabled=true (from string), got %v", config.Enabled)
	}
	// String "123" should convert to int 123
	if config.Count != 123 {
		t.Errorf("Expected Count=123 (from string), got %d", config.Count)
	}
	// Int 456 should convert to string "456"
	if config.Name != "456" {
		t.Errorf("Expected Name=456 (from int), got %s", config.Name)
	}
}

func TestModuleConfigLoader_DomainObjectList(t *testing.T) {
	yamlContent := `
collectors:
  domain:
    domains:
      - endpoint: example.com
        skipTLSVerify: false
      - endpoint: internal.example.local:8443
        skipTLSVerify: true
    checkTimeout: "15s"
`

	tmpFile := createTempYAML(t, yamlContent)
	loader := NewModuleConfigLoader(tmpFile, WithModuleDecodeHook(
		mapstructure.StringToTimeDurationHookFunc(),
	))

	cfg := &DomainModuleTestConfig{}

	err := loader.LoadModuleConfig("collectors.domain", cfg)
	if err != nil {
		t.Fatalf("LoadModuleConfig failed: %v", err)
	}

	if len(cfg.Domains) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(cfg.Domains))
	}

	first, ok := cfg.Domains[0].(map[string]any)
	if !ok {
		t.Fatalf("first domain has unexpected type %T", cfg.Domains[0])
	}

	if first["endpoint"] != "example.com" {
		t.Fatalf("first endpoint = %v, want example.com", first["endpoint"])
	}

	if first["skipTLSVerify"] != false {
		t.Fatalf("first skipTLSVerify = %v, want false", first["skipTLSVerify"])
	}

	second, ok := cfg.Domains[1].(map[string]any)
	if !ok {
		t.Fatalf("second domain has unexpected type %T", cfg.Domains[1])
	}

	if second["endpoint"] != "internal.example.local:8443" {
		t.Fatalf("second endpoint = %v, want internal.example.local:8443", second["endpoint"])
	}

	if second["skipTLSVerify"] != true {
		t.Fatalf("second skipTLSVerify = %v, want true", second["skipTLSVerify"])
	}

	if cfg.CheckTimeout != 15*time.Second {
		t.Fatalf("checkTimeout = %v, want 15s", cfg.CheckTimeout)
	}
}

func TestModuleConfigLoader_DomainMixedList(t *testing.T) {
	yamlContent := `
collectors:
  domain:
    domains:
      - example.com
      - endpoint: internal.example.local:8443
        skipTLSVerify: true
      - api.example.com
`

	tmpFile := createTempYAML(t, yamlContent)
	loader := NewModuleConfigLoader(tmpFile)

	cfg := &DomainModuleTestConfig{}

	err := loader.LoadModuleConfig("collectors.domain", cfg)
	if err != nil {
		t.Fatalf("LoadModuleConfig failed: %v", err)
	}

	if len(cfg.Domains) != 3 {
		t.Fatalf("expected 3 domains, got %d", len(cfg.Domains))
	}

	first, ok := cfg.Domains[0].(string)
	if !ok || first != "example.com" {
		t.Fatalf("unexpected first domain entry: %#v", cfg.Domains[0])
	}

	second, ok := cfg.Domains[1].(map[string]any)
	if !ok {
		t.Fatalf("second domain has unexpected type %T", cfg.Domains[1])
	}

	if second["endpoint"] != "internal.example.local:8443" || second["skipTLSVerify"] != true {
		t.Fatalf("unexpected second domain entry: %#v", second)
	}

	third, ok := cfg.Domains[2].(string)
	if !ok || third != "api.example.com" {
		t.Fatalf("unexpected third domain entry: %#v", cfg.Domains[2])
	}
}

func TestEnvConfigLoader_DomainStringListCompatibility(t *testing.T) {
	loader := NewEnvConfigLoader(WithEnvironment(map[string]string{
		"COLLECTORS_DOMAIN_DOMAINS":      "example.com,api.example.com,www.example.com",
		"COLLECTORS_DOMAIN_INCLUDE_IPV4": "true",
		"COLLECTORS_DOMAIN_INCLUDE_IPV6": "false",
	}))

	cfg := &DomainModuleTestConfig{}

	err := loader.LoadModuleConfig("collectors.domain", cfg)
	if err != nil {
		t.Fatalf("LoadModuleConfig failed: %v", err)
	}

	if !reflect.DeepEqual(cfg.DomainsEnv, []string{
		"example.com",
		"api.example.com",
		"www.example.com",
	}) {
		t.Fatalf("DomainsEnv = %#v, want 3 env domains", cfg.DomainsEnv)
	}

	if !cfg.IncludeIPv4 {
		t.Fatal("IncludeIPv4 = false, want true")
	}

	if cfg.IncludeIPv6 {
		t.Fatal("IncludeIPv6 = true, want false")
	}
}

func TestModuleConfigLoader_NonExistentPath(t *testing.T) {
	yamlContent := `
collectors:
  node:
    enabled: true
`

	tmpFile := createTempYAML(t, yamlContent)

	loader := NewModuleConfigLoader(tmpFile)

	var config TestConfig
	// This should not error, just not load anything
	err := loader.LoadModuleConfig("collectors.nonexistent", &config)
	if err != nil {
		t.Fatalf("LoadModuleConfig should not error on non-existent path: %v", err)
	}

	// Config should remain at zero values
	if config.Enabled {
		t.Errorf("Expected Enabled=false (default), got %v", config.Enabled)
	}

	if config.Count != 0 {
		t.Errorf("Expected Count=0 (default), got %d", config.Count)
	}
}

func TestModuleConfigLoader_EmptyFile(t *testing.T) {
	loader := NewModuleConfigLoader(nil)

	var config TestConfig

	err := loader.LoadModuleConfig("collectors.node", &config)
	if err != nil {
		t.Fatalf("LoadModuleConfig should not error on empty file: %v", err)
	}

	// Config should remain at zero values
	if config.Enabled {
		t.Errorf("Expected Enabled=false (default), got %v", config.Enabled)
	}
}

func TestModuleConfigLoader_NestedPath(t *testing.T) {
	yamlContent := `
server:
  http:
    collectors:
      node:
        enabled: true
        count: 777
`

	tmpFile := createTempYAML(t, yamlContent)

	loader := NewModuleConfigLoader(tmpFile)

	var config TestConfig

	err := loader.LoadModuleConfig("server.http.collectors.node", &config)
	if err != nil {
		t.Fatalf("LoadModuleConfig failed: %v", err)
	}

	if !config.Enabled {
		t.Errorf("Expected Enabled=true, got %v", config.Enabled)
	}

	if config.Count != 777 {
		t.Errorf("Expected Count=777, got %d", config.Count)
	}
}

func TestSplitKey(t *testing.T) {
	t.Helper()

	tests := []struct {
		input    string
		expected []string
	}{
		{"collectors.node", []string{"collectors", "node"}},
		{"a.b.c.d", []string{"a", "b", "c", "d"}},
		{"single", []string{"single"}},
		{"", []string{}},
		{"trailing.", []string{"trailing"}},
		{".leading", []string{"leading"}},
		{"multiple..dots", []string{"multiple", "dots"}},
	}

	for _, tt := range tests {
		result := splitKey(tt.input)
		if !reflect.DeepEqual(result, tt.expected) {
			t.Errorf("splitKey(%q) = %v, expected %v", tt.input, result, tt.expected)
		}
	}
}

// Helper function to create YAML content bytes
func createTempYAML(t *testing.T, content string) []byte {
	t.Helper()
	return []byte(content)
}
