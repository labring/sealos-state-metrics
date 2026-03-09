package domain

import (
	"testing"
	"time"
)

func TestParseDomainTarget(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantHost  string
		wantPort  int
		wantError bool
	}{
		{
			name:     "host only",
			input:    "example.com",
			wantHost: "example.com",
			wantPort: 443,
		},
		{
			name:     "host with custom port",
			input:    "registry.fake.local:5000",
			wantHost: "registry.fake.local",
			wantPort: 5000,
		},
		{
			name:     "ipv6 host with custom port",
			input:    "[::1]:5000",
			wantHost: "::1",
			wantPort: 5000,
		},
		{
			name:     "ipv6 host only with brackets",
			input:    "[::1]",
			wantHost: "::1",
			wantPort: 443,
		},
		{
			name:     "ipv6 host only without brackets",
			input:    "::1",
			wantHost: "::1",
			wantPort: 443,
		},
		{
			name:      "scheme not allowed",
			input:     "https://example.com:5000",
			wantError: true,
		},
		{
			name:      "missing host",
			input:     ":5000",
			wantError: true,
		},
		{
			name:      "missing port value",
			input:     "example.com:",
			wantError: true,
		},
		{
			name:      "invalid port",
			input:     "example.com:abc",
			wantError: true,
		},
		{
			name:      "port out of range",
			input:     "example.com:70000",
			wantError: true,
		},
		{
			name:     "ipv6 literal without brackets",
			input:    "2001:db8::1:5000",
			wantHost: "2001:db8::1:5000",
			wantPort: 443,
		},
		{
			name:      "invalid multi-colon hostname",
			input:     "host:name:5000",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, err := parseDomainTarget(tt.input)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}

			if err != nil {
				t.Fatalf("parseDomainTarget(%q) returned error: %v", tt.input, err)
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
		Domains: []string{
			"example.com",
			" registry.fake.local:5000 ",
			"",
			"   ",
		},
		CheckTimeout:     7 * time.Second,
		CheckInterval:    11 * time.Minute,
		IncludeCertCheck: true,
		IncludeHTTPCheck: false,
	}

	runtimeCfg, err := newRuntimeConfig(cfg)
	if err != nil {
		t.Fatalf("newRuntimeConfig() returned error: %v", err)
	}

	if len(runtimeCfg.domains) != 2 {
		t.Fatalf("expected 2 runtime domains, got %d", len(runtimeCfg.domains))
	}

	first := runtimeCfg.domains[0]
	if first.endpoint != "example.com" || first.target.Host != "example.com" || first.target.Port != 443 {
		t.Fatalf("unexpected first domain: %#v", first)
	}

	second := runtimeCfg.domains[1]
	if second.endpoint != "registry.fake.local:5000" ||
		second.target.Host != "registry.fake.local" ||
		second.target.Port != 5000 {
		t.Fatalf("unexpected second domain: %#v", second)
	}

	if runtimeCfg.checkTimeout != 7*time.Second {
		t.Fatalf("checkTimeout = %v, want %v", runtimeCfg.checkTimeout, 7*time.Second)
	}
	if runtimeCfg.checkInterval != 11*time.Minute {
		t.Fatalf("checkInterval = %v, want %v", runtimeCfg.checkInterval, 11*time.Minute)
	}
	if !runtimeCfg.includeCertCheck {
		t.Fatal("includeCertCheck = false, want true")
	}
	if runtimeCfg.includeHTTPCheck {
		t.Fatal("includeHTTPCheck = true, want false")
	}
}
