package domain

import (
	"context"
	"time"

	"github.com/labring/sealos-state-metrics/pkg/util"
	log "github.com/sirupsen/logrus"
)

// DomainHealth represents the overall health status of a domain
type DomainHealth struct {
	Domain       string
	ResolveOk    bool // Whether DNS resolution succeeded
	IPCount      int  // Number of IPs resolved
	HealthyIPs   int  // Number of healthy IPs (HTTP and/or Cert checks passed)
	UnhealthyIPs int  // Number of unhealthy IPs
	LastChecked  time.Time
}

// IPHealth represents the health status of a specific IP for a domain
type IPHealth struct {
	Domain string
	IP     string // Specific IP address

	// HTTP check
	HTTPOk        bool
	HTTPError     string
	HTTPErrorType ErrorType // Classified error type
	ResponseTime  time.Duration

	// Certificate check
	CertOk        bool
	CertError     string
	CertErrorType ErrorType // Classified error type
	CertExpiry    time.Duration

	LastChecked time.Time
}

// DomainChecker performs health checks on domains
type DomainChecker struct {
	timeout    time.Duration
	checkHTTP  bool
	checkDNS   bool
	checkCert  bool
	classifier *ErrorClassifier
}

// NewDomainChecker creates a new domain checker
func NewDomainChecker(timeout time.Duration, checkHTTP, checkDNS, checkCert bool) *DomainChecker {
	return &DomainChecker{
		timeout:    timeout,
		checkHTTP:  checkHTTP,
		checkDNS:   checkDNS,
		checkCert:  checkCert,
		classifier: NewErrorClassifier(),
	}
}

// CheckIPs performs all enabled checks on a domain for each of its IPs
func (dc *DomainChecker) CheckIPs(
	ctx context.Context,
	domain string,
	logger *log.Entry,
) (*DomainHealth, []*IPHealth) {
	now := time.Now()

	domainHealth := &DomainHealth{
		Domain:      domain,
		ResolveOk:   true,
		LastChecked: now,
	}

	// First, get the IPs for the domain
	var ips []string
	if dc.checkDNS || dc.checkHTTP {
		dnsResult := util.CheckDNS(ctx, domain, dc.timeout)
		if !dnsResult.Success {
			logger.WithFields(log.Fields{
				"domain": domain,
				"error":  dnsResult.Error,
			}).Warn("DNS resolution failed")

			// Mark resolve as failed
			domainHealth.ResolveOk = false
			domainHealth.IPCount = 0
			domainHealth.HealthyIPs = 0

			// Return a health record indicating DNS failure
			return domainHealth, []*IPHealth{
				{
					Domain:        domain,
					IP:            "",
					HTTPOk:        false,
					HTTPError:     "DNS resolution failed: " + dnsResult.Error,
					HTTPErrorType: dc.classifier.ClassifyHTTPError("DNS resolution failed"),
					LastChecked:   now,
				},
			}
		}

		ips = dnsResult.IPs

		// Check if IP list is empty
		if len(ips) == 0 {
			logger.WithFields(log.Fields{
				"domain": domain,
			}).Warn("DNS resolution returned no IPs")

			// Mark as resolved but with 0 IPs
			domainHealth.IPCount = 0
			domainHealth.HealthyIPs = 0

			// Return a health record indicating no IPs
			return domainHealth, []*IPHealth{
				{
					Domain:        domain,
					IP:            "",
					HTTPOk:        false,
					HTTPError:     "DNS resolution returned no IPs",
					HTTPErrorType: dc.classifier.ClassifyHTTPError("no IPs resolved"),
					LastChecked:   now,
				},
			}
		}
	}

	// Get certificate info (shared across all IPs)
	var (
		certInfo *util.CertInfo
		certErr  error
	)

	if dc.checkCert {
		certInfo, certErr = util.GetTLSCert(domain, dc.timeout)
	}

	// Check each IP individually
	results := make([]*IPHealth, 0, len(ips))
	for _, ip := range ips {
		health := &IPHealth{
			Domain:      domain,
			IP:          ip,
			LastChecked: now,
		}

		// HTTP check for this specific IP
		if dc.checkHTTP {
			result := util.CheckHTTPWithIP(ctx, domain, ip, dc.timeout)
			health.HTTPOk = result.Success
			health.HTTPError = result.Error
			health.ResponseTime = result.ResponseTime

			// Classify HTTP error
			if !health.HTTPOk && health.HTTPError != "" {
				health.HTTPErrorType = dc.classifier.ClassifyHTTPError(health.HTTPError)
			} else {
				health.HTTPErrorType = ErrorTypeNone
			}

			logger.WithFields(log.Fields{
				"domain":       domain,
				"ip":           ip,
				"success":      health.HTTPOk,
				"errorType":    health.HTTPErrorType,
				"responseTime": health.ResponseTime,
			}).Debug("HTTP check completed")
		}

		// Certificate check (same for all IPs)
		if dc.checkCert {
			if certErr != nil {
				health.CertOk = false
				health.CertError = certErr.Error()
				health.CertErrorType = dc.classifier.ClassifyCertError(health.CertError)
			} else {
				health.CertOk = certInfo.IsValid
				health.CertExpiry = certInfo.ExpiresIn

				if !certInfo.IsValid {
					health.CertError = "certificate expired or not yet valid"
					health.CertErrorType = ErrorTypeCertExpired
				} else {
					health.CertErrorType = ErrorTypeNone
				}
			}

			logger.WithFields(log.Fields{
				"domain":    domain,
				"ip":        ip,
				"success":   health.CertOk,
				"errorType": health.CertErrorType,
				"expiresIn": health.CertExpiry,
			}).Debug("Certificate check completed")
		}

		results = append(results, health)
	}

	// Calculate domain-level health metrics
	domainHealth.IPCount = len(ips)

	healthyCount := 0
	for _, health := range results {
		// An IP is considered healthy if all enabled checks passed
		isHealthy := true
		if dc.checkHTTP && !health.HTTPOk {
			isHealthy = false
		}

		if dc.checkCert && !health.CertOk {
			isHealthy = false
		}

		if isHealthy {
			healthyCount++
		}
	}

	domainHealth.HealthyIPs = healthyCount
	domainHealth.UnhealthyIPs = domainHealth.IPCount - healthyCount

	return domainHealth, results
}
