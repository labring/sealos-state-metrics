package domain

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

type httpCheckResult struct {
	Success      bool
	ResponseTime time.Duration
	StatusCode   int
	Error        string
}

type httpCheckOptions struct {
	Method              string
	Path                string
	Headers             map[string]string
	ExpectedStatusCodes []int
	FollowRedirects     bool
}

func checkHTTPWithIPAndOptions(
	ctx context.Context,
	host string,
	port int,
	ip string,
	skipTLSVerify bool,
	timeout time.Duration,
	dialRetries int,
	options httpCheckOptions,
) *httpCheckResult {
	if err := validateHTTPMonitoringTarget(host, port, ip); err != nil {
		return &httpCheckResult{
			Success: false,
			Error:   fmt.Sprintf("invalid request target: %v", err),
		}
	}

	requestURL, err := buildHTTPCheckURL(host, port, options.Path)
	if err != nil {
		return &httpCheckResult{
			Success: false,
			Error:   fmt.Sprintf("invalid request path: %v", err),
		}
	}

	method := normalizeHTTPMethod(options.Method)

	defaultDialer := &net.Dialer{
		Timeout: timeout,
	}
	dialContext := retryHTTPDialContext(defaultDialer.DialContext, dialRetries)
	tlsConfig := &tls.Config{
		//nolint:gosec // Domain collector intentionally supports per-target TLS verification bypass.
		InsecureSkipVerify: skipTLSVerify,
		MinVersion:         tls.VersionTLS12,
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialHost, dialPort, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			if sameHTTPHostname(dialHost, host) {
				return dialContext(ctx, network, net.JoinHostPort(ip, dialPort))
			}

			return dialContext(ctx, network, addr)
		},
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialHost, dialPort, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			dialTLSContext := retryHTTPSTLSConnectionContext(
				defaultDialer,
				tlsConfig,
				dialHost,
				dialRetries,
			)
			if sameHTTPHostname(dialHost, host) {
				return dialTLSContext(ctx, network, net.JoinHostPort(ip, dialPort))
			}

			return dialTLSContext(ctx, network, addr)
		},
		TLSClientConfig: tlsConfig,
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
	if !options.FollowRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		return &httpCheckResult{
			Success: false,
			Error:   fmt.Sprintf("failed to create request: %v", err),
		}
	}

	req.Host = formatHTTPSAuthority(host, port)
	for name, value := range options.Headers {
		req.Header.Set(name, value)
	}

	resp, err := client.Do(req)

	responseTime := time.Since(start)
	if err != nil {
		return &httpCheckResult{
			Success:      false,
			ResponseTime: responseTime,
			Error:        fmt.Sprintf("request failed: %v", err),
		}
	}

	defer resp.Body.Close()

	if !isExpectedHTTPStatus(resp.StatusCode, options.ExpectedStatusCodes) {
		return &httpCheckResult{
			Success:      false,
			ResponseTime: responseTime,
			StatusCode:   resp.StatusCode,
			Error:        fmt.Sprintf("unexpected status code: %d", resp.StatusCode),
		}
	}

	return &httpCheckResult{
		Success:      true,
		ResponseTime: responseTime,
		StatusCode:   resp.StatusCode,
	}
}

func buildHTTPCheckURL(host string, port int, path string) (string, error) {
	parsedPath, err := normalizeHTTPPath(path)
	if err != nil {
		return "", err
	}

	return (&url.URL{
		Scheme:   "https",
		Host:     formatHTTPSAuthority(host, port),
		Path:     parsedPath.Path,
		RawQuery: parsedPath.RawQuery,
	}).String(), nil
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

func normalizeHTTPMethod(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return http.MethodGet
	}

	return method
}

func normalizeHTTPPath(path string) (*url.URL, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/"
	}

	if strings.Contains(path, "://") || strings.HasPrefix(path, "//") {
		return nil, errors.New("absolute URLs are not supported")
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	parsedPath, err := url.ParseRequestURI(path)
	if err != nil {
		return nil, err
	}

	if parsedPath.IsAbs() || parsedPath.Host != "" || parsedPath.User != nil {
		return nil, errors.New("absolute URLs are not supported")
	}

	if parsedPath.Path == "" {
		parsedPath.Path = "/"
	}

	return parsedPath, nil
}

func isExpectedHTTPStatus(statusCode int, expected []int) bool {
	if len(expected) == 0 {
		return statusCode >= 200 && statusCode < 500
	}

	return slices.Contains(expected, statusCode)
}

func validateHTTPMonitoringTarget(host string, port int, ip string) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d out of range", port)
	}

	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid IP %q", ip)
	}

	if net.ParseIP(host) == nil {
		if err := validateHTTPHostname(host); err != nil {
			return err
		}
	}

	return nil
}

func validateHTTPHostname(host string) error {
	if strings.TrimSpace(host) == "" {
		return errors.New("host is empty")
	}

	if strings.ContainsAny(host, "/?#@") {
		return fmt.Errorf("host %q contains invalid characters", host)
	}

	return nil
}

func sameHTTPHostname(left, right string) bool {
	return strings.EqualFold(normalizeHTTPHostname(left), normalizeHTTPHostname(right))
}

func normalizeHTTPHostname(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return host
}

type httpDialContextFunc func(context.Context, string, string) (net.Conn, error)

func retryHTTPSTLSConnectionContext(
	netDialer *net.Dialer,
	tlsConfig *tls.Config,
	serverName string,
	retries int,
) httpDialContextFunc {
	return retryHTTPDialContext(
		func(ctx context.Context, network, addr string) (net.Conn, error) {
			attemptTLSConfig := tlsConfig.Clone()
			attemptTLSConfig.ServerName = normalizeHTTPHostname(serverName)

			tlsDialer := &tls.Dialer{
				NetDialer: netDialer,
				Config:    attemptTLSConfig,
			}

			return tlsDialer.DialContext(ctx, network, addr)
		},
		retries,
	)
}

func retryHTTPDialContext(dialContext httpDialContextFunc, retries int) httpDialContextFunc {
	if retries < 1 {
		retries = 1
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		var lastErr error
		for range retries {
			conn, err := dialContext(ctx, network, addr)
			if err == nil {
				return conn, nil
			}

			lastErr = err

			if ctx.Err() != nil {
				break
			}
		}

		return nil, lastErr
	}
}
