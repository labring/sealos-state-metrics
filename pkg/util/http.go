package util

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// HTTPCheckResult contains the result of an HTTP check
type HTTPCheckResult struct {
	Success      bool
	ResponseTime time.Duration
	StatusCode   int
	Error        string
}

// CheckHTTP performs an HTTP/HTTPS health check
func CheckHTTP(ctx context.Context, url string, timeout time.Duration) *HTTPCheckResult {
	parsedURL, err := validateMonitoringURL(url)
	if err != nil {
		return &HTTPCheckResult{
			Success: false,
			Error:   fmt.Sprintf("invalid request target: %v", err),
		}
	}

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false,
				MinVersion:         tls.VersionTLS12,
			},
		},
	}

	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return &HTTPCheckResult{
			Success: false,
			Error:   fmt.Sprintf("failed to create request: %v", err),
		}
	}

	resp, err := client.Do(req)
	responseTime := time.Since(start)

	if err != nil {
		return &HTTPCheckResult{
			Success:      false,
			ResponseTime: responseTime,
			Error:        fmt.Sprintf("request failed: %v", err),
		}
	}

	defer resp.Body.Close()

	return &HTTPCheckResult{
		Success:      resp.StatusCode >= 200 && resp.StatusCode < 500,
		ResponseTime: responseTime,
		StatusCode:   resp.StatusCode,
	}
}

// CheckHTTPWithIP performs an HTTP/HTTPS health check to a specific IP address
func CheckHTTPWithIP(
	ctx context.Context,
	host string,
	port int,
	ip string,
	skipTLSVerify bool,
	timeout time.Duration,
	followRedirects bool,
) *HTTPCheckResult {
	if err := validateMonitoringTarget(host, port, ip); err != nil {
		return &HTTPCheckResult{
			Success: false,
			Error:   fmt.Sprintf("invalid request target: %v", err),
		}
	}

	defaultDialer := &net.Dialer{
		Timeout: timeout,
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialHost, dialPort, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			// Keep the IP-pinned dial only for the configured domain. Redirects to
			// another host fall back to the default dialer so the new destination can
			// resolve and connect normally.
			if sameHostname(dialHost, host) {
				return defaultDialer.DialContext(ctx, network, net.JoinHostPort(ip, dialPort))
			}

			return defaultDialer.DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{
			//nolint:gosec // Domain collector intentionally supports per-target TLS verification bypass.
			InsecureSkipVerify: skipTLSVerify,
			MinVersion:         tls.VersionTLS12,
		},
	}
	defer transport.CloseIdleConnections()

	// Create a transport that dials the specific IP
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
	if !followRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	start := time.Now()

	// Build URL with configured host[:port] while still dialing a specific IP.
	url := (&url.URL{
		Scheme: "https",
		Host:   formatHTTPSAuthority(host, port),
		Path:   "/",
	}).String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &HTTPCheckResult{
			Success: false,
			Error:   fmt.Sprintf("failed to create request: %v", err),
		}
	}

	// Preserve the configured authority for Host routing.
	req.Host = formatHTTPSAuthority(host, port)

	resp, err := client.Do(req)
	responseTime := time.Since(start)

	if err != nil {
		return &HTTPCheckResult{
			Success:      false,
			ResponseTime: responseTime,
			Error:        fmt.Sprintf("request failed: %v", err),
		}
	}

	defer resp.Body.Close()

	success := resp.StatusCode >= 200 && resp.StatusCode < 500
	if !success {
		return &HTTPCheckResult{
			Success:      false,
			ResponseTime: responseTime,
			StatusCode:   resp.StatusCode,
			Error:        fmt.Sprintf("unexpected status code: %d", resp.StatusCode),
		}
	}

	return &HTTPCheckResult{
		Success:      true,
		ResponseTime: responseTime,
		StatusCode:   resp.StatusCode,
	}
}

// GetTLSCertWithIP retrieves the TLS certificate by dialing a specific IP
// while preserving the original host as SNI.
func GetTLSCertWithIP(
	host string,
	port int,
	ip string,
	skipTLSVerify bool,
	timeout time.Duration,
) (*CertInfo, error) {
	if err := validateMonitoringTarget(host, port, ip); err != nil {
		return nil, fmt.Errorf("invalid request target: %w", err)
	}

	return getTLSCert(host, port, ip, skipTLSVerify, timeout)
}

func getTLSCert(
	host string,
	port int,
	dialHost string,
	skipTLSVerify bool,
	timeout time.Duration,
) (*CertInfo, error) {
	dialer := &tls.Dialer{
		Config: &tls.Config{
			//nolint:gosec // Domain collector intentionally supports per-target TLS verification bypass.
			InsecureSkipVerify: skipTLSVerify,
			MinVersion:         tls.VersionTLS12,
			ServerName:         host,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(dialHost, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}
	defer conn.Close()

	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return nil, errors.New("not a TLS connection")
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, errors.New("no certificates found")
	}

	cert := state.PeerCertificates[0]
	now := time.Now()
	expiresIn := cert.NotAfter.Sub(now)
	isValid := now.After(cert.NotBefore) && now.Before(cert.NotAfter)

	return &CertInfo{
		CommonName: cert.Subject.CommonName,
		Issuer:     cert.Issuer.CommonName,
		NotBefore:  cert.NotBefore,
		NotAfter:   cert.NotAfter,
		ExpiresIn:  expiresIn,
		IsValid:    isValid,
	}, nil
}

func formatHTTPSAuthority(host string, port int) string {
	if port == 443 {
		if strings.Contains(host, ":") {
			return "[" + host + "]"
		}
		return host
	}

	return net.JoinHostPort(host, strconv.Itoa(port))
}

func validateMonitoringURL(rawURL string) (*url.URL, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q", parsedURL.Scheme)
	}

	if parsedURL.Host == "" {
		return nil, errors.New("host is empty")
	}

	if parsedURL.User != nil {
		return nil, errors.New("userinfo is not supported")
	}

	hostname := parsedURL.Hostname()
	if hostname == "" {
		return nil, errors.New("hostname is empty")
	}

	if net.ParseIP(hostname) == nil {
		if err := validateHostname(hostname); err != nil {
			return nil, err
		}
	}

	return parsedURL, nil
}

func validateMonitoringTarget(host string, port int, ip string) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d out of range", port)
	}

	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid IP %q", ip)
	}

	if net.ParseIP(host) == nil {
		if err := validateHostname(host); err != nil {
			return err
		}
	}

	return nil
}

func validateHostname(host string) error {
	if strings.TrimSpace(host) == "" {
		return errors.New("host is empty")
	}

	if strings.ContainsAny(host, "/?#@") {
		return fmt.Errorf("host %q contains invalid characters", host)
	}

	return nil
}

func sameHostname(left, right string) bool {
	return strings.EqualFold(normalizeHostname(left), normalizeHostname(right))
}

func normalizeHostname(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return host
}
