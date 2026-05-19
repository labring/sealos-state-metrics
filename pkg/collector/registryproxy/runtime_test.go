//nolint:testpackage // white-box tests intentionally exercise internal parsing helpers
package registryproxy

import (
	"testing"
	"time"
)

func TestNewDefaultConfig(t *testing.T) {
	cfg := NewDefaultConfig()

	if cfg.CheckTimeout != 30*time.Second {
		t.Fatalf("CheckTimeout = %v, want 30s", cfg.CheckTimeout)
	}

	if cfg.CheckInterval != time.Minute {
		t.Fatalf("CheckInterval = %v, want 1m", cfg.CheckInterval)
	}
}

func TestParseRegistryTarget(t *testing.T) {
	tests := []struct {
		name       string
		endpoint   string
		scheme     string
		wantScheme string
		wantHost   string
		wantPort   int
		wantError  bool
	}{
		{
			name:       "host with port",
			endpoint:   "registry.example.com:5000",
			wantScheme: "http",
			wantHost:   "registry.example.com",
			wantPort:   5000,
		},
		{
			name:       "scheme override",
			endpoint:   "registry.example.com:5443",
			scheme:     "https",
			wantScheme: "https",
			wantHost:   "registry.example.com",
			wantPort:   5443,
		},
		{
			name:       "url endpoint",
			endpoint:   "https://registry.example.com:5443",
			wantScheme: "https",
			wantHost:   "registry.example.com",
			wantPort:   5443,
		},
		{
			name:       "ipv4 literal without port",
			endpoint:   "67.21.84.122",
			wantScheme: "http",
			wantHost:   "67.21.84.122",
			wantPort:   5000,
		},
		{
			name:       "ipv6 with port",
			endpoint:   "[::1]:5000",
			wantScheme: "http",
			wantHost:   "::1",
			wantPort:   5000,
		},
		{
			name:      "path not allowed",
			endpoint:  "http://registry.example.com:5000/v2/",
			wantError: true,
		},
		{
			name:      "invalid port",
			endpoint:  "registry.example.com:bad",
			wantError: true,
		},
		{
			name:      "missing host",
			endpoint:  ":5000",
			wantError: true,
		},
		{
			name:      "multi-colon hostname",
			endpoint:  "host:name:5000",
			wantError: true,
		},
		{
			name:      "unsupported scheme",
			endpoint:  "ftp://registry.example.com:5000",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, err := parseRegistryTarget(tt.endpoint, tt.scheme)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error for %q", tt.endpoint)
				}
				return
			}

			if err != nil {
				t.Fatalf("parseRegistryTarget(%q) returned error: %v", tt.endpoint, err)
			}

			if target.Scheme != tt.wantScheme {
				t.Fatalf("target.Scheme = %q, want %q", target.Scheme, tt.wantScheme)
			}

			if target.Host != tt.wantHost {
				t.Fatalf("target.Host = %q, want %q", target.Host, tt.wantHost)
			}

			if target.Port != tt.wantPort {
				t.Fatalf("target.Port = %d, want %d", target.Port, tt.wantPort)
			}
		})
	}
}

func TestNewRuntimeConfig(t *testing.T) {
	cfg := &Config{
		Registries: []any{
			map[string]any{
				"endpoint":             "registry.example.com:5000",
				"info":                 "internal proxy",
				"scheme":               "http",
				"repository":           "/library/alpine/",
				"reference":            "3.20",
				"manifestAcceptHeader": "application/vnd.oci.image.manifest.v1+json",
				"headers": map[string]any{
					"X-Probe": "sealos-state-metrics",
				},
			},
			"67.21.84.122:5000",
			"",
			"   ",
		},
		CheckTimeout:  7 * time.Second,
		CheckInterval: 11 * time.Minute,
	}

	runtimeCfg, err := newRuntimeConfig(cfg)
	if err != nil {
		t.Fatalf("newRuntimeConfig() returned error: %v", err)
	}

	if len(runtimeCfg.registries) != 2 {
		t.Fatalf("expected 2 registries, got %d", len(runtimeCfg.registries))
	}

	first := runtimeCfg.registries[0]
	if first.endpoint != "registry.example.com:5000" ||
		first.info != "internal proxy" ||
		first.target.Scheme != "http" ||
		first.target.Host != "registry.example.com" ||
		first.target.Port != 5000 ||
		first.repository != "library/alpine" ||
		first.reference != "3.20" ||
		first.manifestAcceptHeader != "application/vnd.oci.image.manifest.v1+json" ||
		first.headers["X-Probe"] != "sealos-state-metrics" {
		t.Fatalf("unexpected first registry: %#v", first)
	}

	second := runtimeCfg.registries[1]
	if second.repository != defaultManifestRepo ||
		second.reference != defaultManifestReference ||
		second.manifestAcceptHeader != defaultManifestMediaType {
		t.Fatalf("unexpected second registry defaults: %#v", second)
	}

	if runtimeCfg.checkTimeout != 7*time.Second {
		t.Fatalf("checkTimeout = %v, want 7s", runtimeCfg.checkTimeout)
	}

	if runtimeCfg.checkInterval != 11*time.Minute {
		t.Fatalf("checkInterval = %v, want 11m", runtimeCfg.checkInterval)
	}
}

func TestNewRuntimeConfigRegistriesEnvOverride(t *testing.T) {
	cfg := &Config{
		Registries: []any{
			"from-file.example.com:5000",
		},
		RegistriesEnv: []string{
			"from-env.example.com:5000",
		},
	}

	runtimeCfg, err := newRuntimeConfig(cfg)
	if err != nil {
		t.Fatalf("newRuntimeConfig() returned error: %v", err)
	}

	if len(runtimeCfg.registries) != 1 {
		t.Fatalf("expected 1 registry, got %d", len(runtimeCfg.registries))
	}

	if runtimeCfg.registries[0].endpoint != "from-env.example.com:5000" {
		t.Fatalf("endpoint = %q, want env override", runtimeCfg.registries[0].endpoint)
	}
}

func TestRegistryURLs(t *testing.T) {
	registry, err := parseMonitoredRegistry(map[string]any{
		"endpoint":   "registry.example.com:5000",
		"repository": "/library/busybox/",
		"reference":  "sha256:abc123",
	})
	if err != nil {
		t.Fatalf("parseMonitoredRegistry returned error: %v", err)
	}

	if got := registryAPIURL(registry); got != "http://registry.example.com:5000/v2/" {
		t.Fatalf("registryAPIURL() = %q", got)
	}

	wantManifest := "http://registry.example.com:5000/v2/library/busybox/manifests/sha256:abc123"
	if got := registryManifestURL(registry); got != wantManifest {
		t.Fatalf("registryManifestURL() = %q, want %q", got, wantManifest)
	}
}
