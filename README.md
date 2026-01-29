# Sealos State Metrics

[![Go Report Card](https://goreportcard.com/badge/github.com/labring/sealos-state-metric)](https://goreportcard.com/report/github.com/labring/sealos-state-metric)
[![License](https://img.shields.io/github/license/labring/sealos-state-metric)](https://github.com/labring/sealos-state-metric/blob/main/LICENSE)

A Kubernetes metrics collector for monitoring cluster state and resources. Provides Prometheus-compatible metrics for various aspects of your Kubernetes infrastructure.

## Features

- **Modular Collector Architecture**: Enable only the collectors you need
- **DaemonSet Deployment**: Runs on every node for comprehensive monitoring
- **Leader Election Support**: Cluster-level collectors use leader election to avoid duplicate metrics
- **Hot Configuration Reload**: Update configuration without pod restarts
- **Prometheus Integration**: Native Prometheus metrics format with ServiceMonitor support
- **VictoriaMetrics Support**: VMServiceScrape integration for VictoriaMetrics users

## Available Collectors

| Collector | Description | Leader Election |
|-----------|-------------|-----------------|
| `domain` | Domain health and certificate monitoring | Yes |
| `node` | Kubernetes node metrics | Yes |
| `imagepull` | Container image pull performance tracking | Yes |
| `zombie` | Zombie process detection | Yes |
| `cloudbalance` | Cloud provider account balance monitoring | Yes |
| `lvm` | LVM storage metrics (node-level) | No |

## Quick Start

### Installation via Helm

```bash
# Add the Helm repository (if available)
helm repo add sealos-state-metrics https://charts.sealos.io
helm repo update

# Install with default configuration (LVM collector enabled)
helm install state-metrics sealos-state-metrics/state-metrics \
  --namespace monitoring \
  --create-namespace

# Install with custom collectors
helm install state-metrics sealos-state-metrics/state-metrics \
  --namespace monitoring \
  --create-namespace \
  --set enabledCollectors={domain,node,lvm}
```

### Installation from Source

```bash
# Clone the repository
git clone https://github.com/labring/sealos-state-metric.git
cd sealos-state-metric

# Install using local Helm chart
helm install sealos-state-metrics ./deploy/charts/sealos-state-metrics \
  --namespace monitoring \
  --create-namespace \
  --set enabledCollectors={lvm}
```

## Configuration

### Basic Configuration

Edit `values.yaml` or use `--set` flags:

```yaml
# Enable collectors
enabledCollectors:
  - domain
  - node
  - lvm

# Leader election (for cluster-level collectors)
leaderElection:
  enabled: true
  namespace: ""
  leaseName: "sealos-state-metrics"
  leaseDuration: "15s"

# Metrics namespace (optional prefix for all metrics)
config:
  metrics:
    namespace: ""  # Set to "sealos" for sealos_* prefixed metrics

# Logging
config:
  logging:
    level: "info"
    format: "json"
```

### Collector-Specific Configuration

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

  lvm:
    updateInterval: "10s"

  cloudbalance:
    checkInterval: "5m"
    accounts:
      - provider: "aliyun"
        accessKeyId: "xxx"
        accessKeySecret: "yyy"
```

### Resource Limits

```yaml
resources:
  limits:
    cpu: 500m
    memory: 256Mi
  requests:
    cpu: 50m
    memory: 64Mi
```

## Monitoring Integration

### Prometheus Operator

Enable ServiceMonitor:

```yaml
serviceMonitor:
  enabled: true
  namespace: monitoring
  interval: 30s
  scrapeTimeout: 10s
```

### VictoriaMetrics Operator

Enable VMServiceScrape:

```yaml
vmServiceScrape:
  enabled: true
  namespace: monitoring
  interval: 30s
  scrapeTimeout: 10s
```

### Manual Prometheus Configuration

```yaml
scrape_configs:
  - job_name: 'sealos-state-metrics'
    kubernetes_sd_configs:
      - role: endpoints
        namespaces:
          names:
            - monitoring
    relabel_configs:
      - source_labels: [__meta_kubernetes_service_name]
        action: keep
        regex: sealos-state-metrics
```

## Metrics Examples

### LVM Metrics

```
lvm_vgs_total_capacity{instance="node-1",node="node-1"} 6.72149798912e+11
lvm_vgs_total_free{instance="node-1",node="node-1"} 0
```

### Framework Metrics

```
state_metric_collector_duration_seconds{collector="lvm",instance="node-1"} 1.0861e-05
state_metric_collector_success{collector="lvm",instance="node-1"} 1
```

## Development

### Building

```bash
# Build binary
make build

# Build Docker image
make docker-build

# Run tests
make test

# Run linters
make lint
```

### Adding a New Collector

1. Create a new package under `pkg/collector/<name>/`
2. Implement the `Collector` interface
3. Register in `pkg/collector/all/all.go`
4. Add configuration to `values.yaml`
5. Update documentation

## Architecture

- **DaemonSet Deployment**: All collectors run as a DaemonSet across the cluster
- **Leader Election**: Cluster-level collectors (domain, node, etc.) use leader election to ensure only one instance actively collects metrics
- **Node-Level Collectors**: Collectors like LVM run on each node independently without leader election
- **Identity System**: Each pod uses NODE_NAME as its identity for proper metric labeling
- **Hot Reload**: Configuration changes trigger graceful reloads without downtime

## Troubleshooting

### Check Pod Status

```bash
kubectl get pods -n monitoring -l app.kubernetes.io/name=sealos-state-metrics
```

### View Logs

```bash
kubectl logs -n monitoring -l app.kubernetes.io/name=sealos-state-metrics --tail=100
```

### Access Metrics Endpoint

```bash
kubectl port-forward -n monitoring svc/sealos-state-metrics 9090:9090
curl http://localhost:9090/metrics
```

### Leader Election Status

```bash
kubectl get lease -n monitoring sealos-state-metrics -o yaml
```

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.

## Contributing

Contributions are welcome! Please read our [Contributing Guide](CONTRIBUTING.md) for details.

## Support

- Issues: [GitHub Issues](https://github.com/labring/sealos-state-metric/issues)
- Documentation: [Wiki](https://github.com/labring/sealos-state-metric/wiki)
- Community: [Sealos Community](https://github.com/labring/sealos)
