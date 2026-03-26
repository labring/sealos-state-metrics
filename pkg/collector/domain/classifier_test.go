//nolint:testpackage // Tests exercise package internals alongside exported classifier behavior.
package domain

import "testing"

func TestClassifyCertError(t *testing.T) {
	classifier := NewErrorClassifier()

	tests := []struct {
		name  string
		input string
		want  ErrorType
	}{
		{
			name:  "timeout",
			input: "failed to dial: context deadline exceeded",
			want:  ErrorTypeTimeout,
		},
		{
			name:  "network unreachable",
			input: "failed to dial: dial tcp [2606:4700::6812:1a78]:443: connect: network is unreachable",
			want:  ErrorTypeNetworkError,
		},
		{
			name:  "connection reset",
			input: "failed to dial: read tcp 10.0.0.1:12345->1.2.3.4:443: read: connection reset by peer",
			want:  ErrorTypeNetworkError,
		},
		{
			name:  "connection refused",
			input: "failed to dial: dial tcp 1.2.3.4:443: connect: connection refused",
			want:  ErrorTypeConnectionRefused,
		},
		{
			name:  "hostname mismatch",
			input: "x509: certificate is valid for foo.example.com, not example.com",
			want:  ErrorTypeCertHostnameMismatch,
		},
		{
			name:  "certificate verify",
			input: "tls: failed to verify certificate: x509: certificate signed by unknown authority",
			want:  ErrorTypeSSLError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifier.ClassifyCertError(tt.input)
			if got != tt.want {
				t.Fatalf("ClassifyCertError(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestClassifyDNSError(t *testing.T) {
	classifier := NewErrorClassifier()

	tests := []struct {
		name  string
		input string
		want  ErrorType
	}{
		{
			name:  "no such host",
			input: "DNS lookup failed: lookup internal.example.local: no such host",
			want:  ErrorTypeDNSNoSuchHost,
		},
		{
			name:  "no resolved ip",
			input: "DNS resolution returned no IPs after IP family filtering",
			want:  ErrorTypeDNSNoAnswer,
		},
		{
			name:  "timeout",
			input: "DNS lookup failed: context deadline exceeded",
			want:  ErrorTypeTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifier.ClassifyDNSError(tt.input)
			if got != tt.want {
				t.Fatalf("ClassifyDNSError(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
