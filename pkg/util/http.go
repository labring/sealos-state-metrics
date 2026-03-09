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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	timeout time.Duration,
) *HTTPCheckResult {
	// Create a transport that dials the specific IP
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				// Override the address with our specific IP
				return (&net.Dialer{
					Timeout: timeout,
				}).DialContext(ctx, network, net.JoinHostPort(ip, strconv.Itoa(port)))
			},
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false,
				MinVersion:         tls.VersionTLS12,
				ServerName:         host,
			},
		},
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

	return &HTTPCheckResult{
		Success:      resp.StatusCode >= 200 && resp.StatusCode < 500,
		ResponseTime: responseTime,
		StatusCode:   resp.StatusCode,
	}
}

// GetTLSCert retrieves the TLS certificate from a domain
func GetTLSCert(host string, port int, timeout time.Duration) (*CertInfo, error) {
	dialer := &tls.Dialer{
		Config: &tls.Config{
			InsecureSkipVerify: false,
			MinVersion:         tls.VersionTLS12,
			ServerName:         host,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
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
