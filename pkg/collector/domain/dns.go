package domain

import (
	"context"
	"fmt"
	"net"
	"time"
)

type ipFamilyFilter struct {
	IncludeIPv4 bool
	IncludeIPv6 bool
}

type dnsCheckResult struct {
	Success bool
	IPs     []string
	Error   string
}

func checkDNSWithFilter(
	ctx context.Context,
	domain string,
	timeout time.Duration,
	filter ipFamilyFilter,
) *dnsCheckResult {
	resolver := &net.Resolver{}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ips, err := resolver.LookupHost(ctx, domain)
	if err != nil {
		return &dnsCheckResult{
			Success: false,
			Error:   fmt.Sprintf("DNS lookup failed: %v", err),
		}
	}

	filteredIPs := filterIPsByFamily(ips, filter)

	return &dnsCheckResult{
		Success: len(filteredIPs) > 0,
		IPs:     filteredIPs,
	}
}

func filterIPsByFamily(ips []string, filter ipFamilyFilter) []string {
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
