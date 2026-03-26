//nolint:testpackage // Tests need access to collector internals for metric emission verification.
package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	log "github.com/sirupsen/logrus"
)

func TestCollectSkipsUncheckedStatusesAndUsesEmptyErrorTypeOnSuccess(t *testing.T) {
	c := &Collector{
		BaseCollector: base.NewBaseCollector("domain", log.NewEntry(log.New())),
		runtime: &runtimeConfig{
			includeHTTPCheck: true,
			includeCertCheck: true,
		},
		ips: map[string]*IPHealth{
			"example.com/104.18.26.120": {
				Domain:        "example.com",
				IP:            "104.18.26.120",
				DNSChecked:    true,
				DNSOk:         true,
				DNSErrorType:  "",
				HTTPChecked:   true,
				HTTPOk:        true,
				HTTPErrorType: "",
				ResponseTime:  100 * time.Millisecond,
				CertChecked:   true,
				CertOk:        true,
				CertErrorType: "",
				CertExpiry:    24 * time.Hour,
			},
			"internal.example.local:8443/": {
				Domain:        "internal.example.local:8443",
				IP:            "",
				DNSChecked:    true,
				DNSOk:         false,
				DNSErrorType:  ErrorTypeDNSError,
				CertChecked:   false,
				CertOk:        false,
				CertErrorType: "",
			},
		},
		domains: map[string]*DomainHealth{},
	}
	c.initMetrics("test")

	ch := make(chan prometheus.Metric, 16)
	c.collect(ch)
	close(ch)

	var (
		httpSuccessFound bool
		certSuccessFound bool
		dnsSuccessFound  bool
		dnsFailureFound  bool
		dnsFailureType   string
		dnsCertFound     bool
		dnsHTTPFound     bool
	)

	for metric := range ch {
		descText := metric.Desc().String()

		var dtoMetric dto.Metric
		if err := metric.Write(&dtoMetric); err != nil {
			t.Fatalf("metric.Write() failed: %v", err)
		}

		labels := make(map[string]string, len(dtoMetric.GetLabel()))
		for _, label := range dtoMetric.GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}

		checkType := labels["check_type"]
		domain := labels["domain"]
		ip := labels["ip"]
		errorType := labels["error_type"]

		if strings.Contains(descText, `fqName: "test_domain_dns_status"`) &&
			domain == "example.com" && errorType == "" {
			dnsSuccessFound = true
		}

		if strings.Contains(descText, `fqName: "test_domain_dns_status"`) &&
			domain == "internal.example.local:8443" {
			dnsFailureFound = true
			dnsFailureType = errorType
		}

		if domain == "example.com" && ip == "104.18.26.120" && checkType == "http" {
			httpSuccessFound = true

			if errorType != "" {
				t.Fatalf("http success error_type = %q, want empty", errorType)
			}
		}

		if domain == "example.com" && ip == "104.18.26.120" && checkType == "cert" {
			certSuccessFound = true

			if errorType != "" {
				t.Fatalf("cert success error_type = %q, want empty", errorType)
			}
		}

		if domain == "internal.example.local:8443" && checkType == "cert" {
			dnsCertFound = true
		}

		if domain == "internal.example.local:8443" && checkType == "http" {
			dnsHTTPFound = true
		}
	}

	if !httpSuccessFound {
		t.Fatal("expected successful HTTP status metric")
	}

	if !certSuccessFound {
		t.Fatal("expected successful cert status metric")
	}

	if !dnsSuccessFound {
		t.Fatal("expected successful DNS status metric")
	}

	if !dnsFailureFound {
		t.Fatal("expected DNS failure status metric")
	}

	if dnsFailureType != string(ErrorTypeDNSError) {
		t.Fatalf("dns failure error_type = %q, want %q", dnsFailureType, ErrorTypeDNSError)
	}

	if dnsCertFound {
		t.Fatal("unexpected cert status metric for unchecked DNS failure case")
	}

	if dnsHTTPFound {
		t.Fatal("unexpected http status metric for unchecked DNS failure case")
	}
}
