# Domain Collector

The Domain collector monitors domain health by performing DNS lookups, HTTP checks, and certificate validation.

## Features

- **Domain-level metrics**: Aggregate health status for each domain
- **IP-level metrics**: Detailed health status for each resolved IP
- **DNS resolution tracking**: Monitors DNS resolution success and IP counts
- **Failure exposure**: DNS resolution failures and empty IP lists are exposed as unhealthy metrics
- **Concurrent checks**: Checks multiple domains concurrently for efficiency
- **Error classification**: Categorizes errors for better alerting and debugging

## Configuration

### YAML Configuration

```yaml
collectors:
  domain:
    domains:
      - example.com
      - api.example.com
    checkTimeout: "5s"
    checkInterval: "5m"
    includeCertCheck: true
    includeHTTPCheck: true
```

### Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `domains` | []string | `[]` | List of domains to monitor |
| `checkTimeout` | duration | `5s` | Timeout for each health check |
| `checkInterval` | duration | `5m` | Interval between check cycles |
| `includeCertCheck` | bool | `true` | Enable TLS certificate validation |
| `includeHTTPCheck` | bool | `true` | Enable HTTP connectivity checks |

### Environment Variables

All configuration can be overridden using environment variables with the prefix `COLLECTORS_DOMAIN_`:

| Environment Variable | Maps To | Example |
|---------------------|---------|---------|
| `COLLECTORS_DOMAIN_DOMAINS` | `domains` | `example.com,api.example.com` |
| `COLLECTORS_DOMAIN_CHECK_TIMEOUT` | `checkTimeout` | `10s` |
| `COLLECTORS_DOMAIN_CHECK_INTERVAL` | `checkInterval` | `10m` |
| `COLLECTORS_DOMAIN_INCLUDE_CERT_CHECK` | `includeCertCheck` | `true` |
| `COLLECTORS_DOMAIN_INCLUDE_HTTP_CHECK` | `includeHTTPCheck` | `false` |

## Metrics

### `sealos_domain_health`

**Type:** Gauge
**Labels:**
- `domain`: Domain name being monitored
- `type`: Metric type (`resolve`, `ip_count`, `healthy_ips`, `unhealthy_ips`)

**Description:** Domain-level health metrics providing an overview of the domain's resolution and IP health status.

**Metric Types:**
- `resolve`: DNS resolution status (1=success, 0=failure)
- `ip_count`: Total number of IPs resolved for the domain
- `healthy_ips`: Number of IPs that passed all enabled health checks
- `unhealthy_ips`: Number of IPs that failed one or more health checks

**Example:**
```promql
# DNS resolution successful, 2 IPs resolved, all healthy
sealos_domain_health{domain="example.com",type="resolve"} 1
sealos_domain_health{domain="example.com",type="ip_count"} 2
sealos_domain_health{domain="example.com",type="healthy_ips"} 2
sealos_domain_health{domain="example.com",type="unhealthy_ips"} 0

# DNS resolution failed
sealos_domain_health{domain="bad.example.com",type="resolve"} 0
sealos_domain_health{domain="bad.example.com",type="ip_count"} 0
sealos_domain_health{domain="bad.example.com",type="healthy_ips"} 0
sealos_domain_health{domain="bad.example.com",type="unhealthy_ips"} 0

# DNS resolution successful but no IPs returned
sealos_domain_health{domain="noip.example.com",type="resolve"} 1
sealos_domain_health{domain="noip.example.com",type="ip_count"} 0
sealos_domain_health{domain="noip.example.com",type="healthy_ips"} 0
sealos_domain_health{domain="noip.example.com",type="unhealthy_ips"} 0

# 3 IPs resolved, 1 unhealthy
sealos_domain_health{domain="partial.example.com",type="resolve"} 1
sealos_domain_health{domain="partial.example.com",type="ip_count"} 3
sealos_domain_health{domain="partial.example.com",type="healthy_ips"} 2
sealos_domain_health{domain="partial.example.com",type="unhealthy_ips"} 1
```

**Common Queries:**
```promql
# Domains with DNS resolution failures
sealos_domain_health{type="resolve"} == 0

# Domains with no IPs
sealos_domain_health{type="ip_count"} == 0

# Domains with unhealthy IPs
sealos_domain_health{type="unhealthy_ips"} > 0

# Domain health ratio (percentage of healthy IPs)
sealos_domain_health{type="healthy_ips"} / sealos_domain_health{type="ip_count"} * 100
```

### `sealos_domain_status`

**Type:** Gauge
**Labels:**
- `domain`: Domain name being monitored
- `ip`: Resolved IP address
- `check_type`: Type of check performed (`dns`, `cert`, `http`)
- `error_type`: Error type if check failed (empty if successful)

**Values:**
- `1`: Check passed
- `0`: Check failed

**Example:**
```promql
# Healthy domain with successful checks
sealos_domain_status{domain="example.com",ip="93.184.216.34",check_type="http",error_type=""} 1
sealos_domain_status{domain="example.com",ip="93.184.216.34",check_type="cert",error_type=""} 1

# DNS resolution failure (ip is empty string)
sealos_domain_status{domain="bad.example.com",ip="",check_type="http",error_type="dns"} 0

# No IPs resolved (ip is empty string)
sealos_domain_status{domain="noip.example.com",ip="",check_type="http",error_type="dns"} 0

# HTTP check failure for specific IP
sealos_domain_status{domain="slow.example.com",ip="1.2.3.4",check_type="http",error_type="timeout"} 0
```

### `sealos_domain_cert_expiry_seconds`

**Type:** Gauge
**Labels:**
- `domain`: Domain name being monitored
- `ip`: IP address of the endpoint
- `error_type`: Error type if cert check failed

**Description:** Time in seconds until the TLS certificate expires. Negative values indicate expired certificates.

**Example:**
```promql
sealos_domain_cert_expiry_seconds{domain="example.com",ip="93.184.216.34",error_type=""} 7776000
sealos_domain_cert_expiry_seconds{domain="expired.example.com",ip="1.2.3.4",error_type=""} -86400
```

### `sealos_domain_response_time_seconds`

**Type:** Gauge
**Labels:**
- `domain`: Domain name being monitored
- `ip`: IP address of the endpoint

**Description:** Response time for the domain health check in seconds.

**Example:**
```promql
sealos_domain_response_time_seconds{domain="example.com",ip="93.184.216.34"} 0.125
```

## Health Check Logic

### IP Health Determination

An IP is considered **healthy** if:

- All enabled checks pass successfully
- If `includeHTTPCheck=true`: HTTP check must succeed
- If `includeCertCheck=true`: Certificate check must succeed

An IP is considered **unhealthy** if:

- Any enabled check fails

### DNS Resolution Failures

When DNS resolution fails or returns no IPs:

- `sealos_domain_health{type="resolve"}` is set to `0` (for DNS failures) or `1` (for empty IP list)
- `sealos_domain_health{type="ip_count"}` is set to `0`
- `sealos_domain_status` metrics are still exposed with `ip=""` to indicate the issue
- This ensures monitoring systems are always aware of domain problems

### Example Scenarios

1. **Healthy domain**: All IPs pass all checks
   - `healthy_ips` = total IPs
   - `unhealthy_ips` = 0

2. **DNS failure**: Cannot resolve domain
   - `resolve` = 0
   - `ip_count` = 0
   - Metrics still exposed with empty IP

3. **Partial failure**: Some IPs fail checks
   - `resolve` = 1
   - `healthy_ips` < `ip_count`
   - `unhealthy_ips` > 0

4. **Empty resolution**: DNS succeeds but no IPs
   - `resolve` = 1
   - `ip_count` = 0
   - Metrics exposed with empty IP

## Collector Type

**Type:** Polling
**Leader Election Required:** No

The Domain collector runs independently on each node and polls configured domains at regular intervals.
