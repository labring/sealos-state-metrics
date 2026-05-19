# Cockroach License Collector

The `cockroachlicense` collector connects to CockroachDB through the PostgreSQL
wire protocol, reads `SHOW CLUSTER SETTING enterprise.license`, decodes the
license locally, and exposes license metadata as Prometheus metrics.

The raw license string is never exported.

## Configuration

```yaml
enabledCollectors:
  - cockroachlicense

collectors:
  cockroachlicense:
    checkInterval: "5m"
    checkTimeout: "10s"
    clusters:
      - name: "main"
        dsn: "postgresql://root@cockroach-public.default.svc:26257/defaultdb?sslmode=verify-full&sslrootcert=/certs/ca.crt&sslcert=/certs/client.root.crt&sslkey=/certs/client.root.key"
```

For insecure test clusters, use `sslmode=disable`.

## Metrics

- `sealos_cockroach_license_up`
- `sealos_cockroach_license_info`
- `sealos_cockroach_license_type`
- `sealos_cockroach_license_expiry_timestamp_seconds`
- `sealos_cockroach_license_seconds_until_expiry`
- `sealos_cockroach_license_last_scrape_timestamp_seconds`

