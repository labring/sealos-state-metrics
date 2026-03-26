package util

import (
	"context"
	"fmt"
	"net"
	"time"
)

type IPFamilyFilter struct {
	IncludeIPv4 bool
	IncludeIPv6 bool
}

// DNSCheckResult contains the result of a DNS check
type DNSCheckResult struct {
	Success bool
	IPs     []string
	Error   string
}

// CheckDNS performs a DNS lookup
func CheckDNS(ctx context.Context, domain string, timeout time.Duration) *DNSCheckResult {
	return CheckDNSWithFilter(ctx, domain, timeout, IPFamilyFilter{
		IncludeIPv4: true,
		IncludeIPv6: true,
	})
}

// CheckDNSWithFilter performs a DNS lookup and filters IPs by family.
func CheckDNSWithFilter(
	ctx context.Context,
	domain string,
	timeout time.Duration,
	filter IPFamilyFilter,
) *DNSCheckResult {
	resolver := &net.Resolver{}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ips, err := resolver.LookupHost(ctx, domain)
	if err != nil {
		return &DNSCheckResult{
			Success: false,
			Error:   fmt.Sprintf("DNS lookup failed: %v", err),
		}
	}

	filteredIPs := filterIPsByFamily(ips, filter)

	return &DNSCheckResult{
		Success: len(filteredIPs) > 0,
		IPs:     filteredIPs,
	}
}

func filterIPsByFamily(ips []string, filter IPFamilyFilter) []string {
	if filter.IncludeIPv4 && filter.IncludeIPv6 {
		return ips
	}

	filtered := make([]string, 0, len(ips))
	for _, ip := range ips {
		parsedIP := net.ParseIP(ip)
		if parsedIP == nil {
			continue
		}

		isIPv4 := parsedIP.To4() != nil
		if isIPv4 && filter.IncludeIPv4 {
			filtered = append(filtered, ip)
			continue
		}

		if !isIPv4 && filter.IncludeIPv6 {
			filtered = append(filtered, ip)
		}
	}

	return filtered
}

// CheckIPReachability checks if an IP is reachable
func CheckIPReachability(ctx context.Context, ip string, port int, timeout time.Duration) bool {
	dialer := &net.Dialer{
		Timeout: timeout,
	}

	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", ip, port))
	if err != nil {
		return false
	}
	defer conn.Close()

	return true
}
