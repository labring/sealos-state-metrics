# RegistryProxy Collector

The RegistryProxy collector monitors Docker registry proxy availability by calling the Docker Registry HTTP API.

## Features

- Checks `/v2/` API connectivity for each configured registry proxy
- Checks whether a configured image manifest can be fetched
- Resolves the configured domain or address and emits metrics per resolved IP
- Adds configured metadata (`info`, repository, reference, scheme, host, port) to metric labels
- Uses the same polling lifecycle as other collectors

## Configuration

### YAML Configuration

```yaml
collectors:
  registryproxy:
    registries:
      - endpoint: 67.21.84.122:5000
        info: public mirror proxy
        scheme: http
        repository: library/busybox
        reference: latest
        manifestAcceptHeader: application/vnd.docker.distribution.manifest.v2+json
      - endpoint: registry-proxy.example.com:5000
        info: internal registry proxy
        scheme: http
        headers:
          X-Probe: sealos-state-metrics
      - mirror.example.com:5000
    checkTimeout: 30s
    checkInterval: 1m
```

`registries` supports mixed entries:

- String entries such as `67.21.84.122:5000` or `registry-proxy.example.com:5000`
- Object entries with endpoint metadata and manifest options

### Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `registries` | `[]string` or `[]object` | `[]` | Registry proxy endpoints to monitor |
| `registries[].endpoint` | string | - | Registry proxy endpoint. Supports `host:port`, `http://host:port`, or `https://host:port` |
| `registries[].info` | string | `""` | Free-form metadata emitted as the `info` label |
| `registries[].scheme` | string | `http` | Request scheme when `endpoint` does not include one |
| `registries[].repository` | string | `library/busybox` | Repository used for the manifest check |
| `registries[].reference` | string | `latest` | Tag or digest used for the manifest check |
| `registries[].manifestAcceptHeader` | string | `application/vnd.docker.distribution.manifest.v2+json` | `Accept` header used for the manifest check |
| `registries[].headers` | map[string]string | `{}` | Extra HTTP headers added to API and manifest requests |
| `checkTimeout` | duration | `30s` | Timeout for each API request |
| `checkInterval` | duration | `1m` | Interval between check cycles |

### Environment Variables

All configuration can be overridden using environment variables with the prefix `COLLECTORS_REGISTRYPROXY_`:

| Environment Variable | Maps To | Example |
|---------------------|---------|---------|
| `COLLECTORS_REGISTRYPROXY_REGISTRIES` | `registries` | `67.21.84.122:5000,registry-proxy.example.com:5000` |
| `COLLECTORS_REGISTRYPROXY_CHECK_TIMEOUT` | `checkTimeout` | `10s` |
| `COLLECTORS_REGISTRYPROXY_CHECK_INTERVAL` | `checkInterval` | `1m` |

Notes:

- `COLLECTORS_REGISTRYPROXY_REGISTRIES` only supports comma-separated string entries.
- If `COLLECTORS_REGISTRYPROXY_REGISTRIES` is set, it overrides the YAML `registries` list.
- `info`, `scheme`, `repository`, `reference`, `manifestAcceptHeader`, and `headers` are only configurable through YAML object entries.

## Metrics

### `sealos_registry_proxy_health`

**Type:** Gauge

**Labels:**
- `endpoint`: Configured registry proxy endpoint
- `scheme`: Request scheme
- `host`: Parsed host
- `port`: Parsed port
- `info`: Configured info metadata
- `repository`: Repository used for manifest checks
- `reference`: Tag or digest used for manifest checks
- `type`: Metric type (`resolve`, `ip_count`, `healthy_ips`, `unhealthy_ips`)

**Metric Types:**
- `resolve`: DNS resolution status (1=success, 0=failure)
- `ip_count`: Number of IPs resolved for the endpoint
- `healthy_ips`: Number of IPs that passed both API and manifest checks
- `unhealthy_ips`: Number of IPs that failed one or more checks

### `sealos_registry_proxy_dns_status`

**Type:** Gauge

**Labels:** `endpoint`, `scheme`, `host`, `port`, `info`, `repository`, `reference`, `error_type`

**Values:**
- `1`: DNS resolution succeeded
- `0`: DNS resolution failed

### `sealos_registry_proxy_status`

**Type:** Gauge

**Labels:** `endpoint`, `scheme`, `host`, `port`, `info`, `repository`, `reference`, `ip`, `check_type`, `status_code`, `error_type`

**Values:**
- `1`: Check passed
- `0`: Check failed

`check_type` is one of:

- `api`: `GET /v2/`
- `manifest`: `GET /v2/<repository>/manifests/<reference>`

The `/v2/` API check treats any response below `500` as available. This keeps unauthenticated `401 Unauthorized` registry responses available because they prove that the registry API is reachable. The manifest check requires a `2xx` response.

### `sealos_registry_proxy_response_time_seconds`

**Type:** Gauge

**Labels:** `endpoint`, `scheme`, `host`, `port`, `info`, `repository`, `reference`, `ip`, `check_type`

**Description:** Response time for successful or failed HTTP requests that reached the HTTP client.

## Example Checks

Equivalent manual checks:

```bash
curl -i http://67.21.84.122:5000/v2/
curl -fsSL \
  -H 'Accept: application/vnd.docker.distribution.manifest.v2+json' \
  http://67.21.84.122:5000/v2/library/busybox/manifests/latest
```
