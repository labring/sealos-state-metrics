# sealos-state-metric Helm Chart

Kubernetes state metrics collector for Sealos monitoring.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- Prometheus Operator (optional, for ServiceMonitor)

## Installing the Chart

To install the chart with the release name `sealos-state-metric`:

```bash
helm install sealos-state-metric ./deploy/charts/sealos-state-metric
```

To install in a specific namespace:

```bash
helm install sealos-state-metric ./deploy/charts/sealos-state-metric -n sealos-system --create-namespace
```

## Uninstalling the Chart

```bash
helm uninstall sealos-state-metric
```

## Configuration

The following table lists the configurable parameters of the chart and their default values.

### Global Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of replicas | `1` |
| `image.repository` | Image repository | `ghcr.io/labring/sealos-state-metric` |
| `image.tag` | Image tag | `""` (uses appVersion) |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |

### Service Account

| Parameter | Description | Default |
|-----------|-------------|---------|
| `serviceAccount.create` | Create service account | `true` |
| `serviceAccount.name` | Service account name | `""` |
| `serviceAccount.annotations` | Service account annotations | `{}` |

### RBAC

| Parameter | Description | Default |
|-----------|-------------|---------|
| `rbac.create` | Create RBAC resources | `true` |

### Service

| Parameter | Description | Default |
|-----------|-------------|---------|
| `service.type` | Service type | `ClusterIP` |
| `service.port` | Service port | `9090` |
| `service.targetPort` | Container port | `9090` |

### ServiceMonitor (Prometheus Operator)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `serviceMonitor.enabled` | Create ServiceMonitor | `false` |
| `serviceMonitor.interval` | Scrape interval | `30s` |
| `serviceMonitor.scrapeTimeout` | Scrape timeout | `10s` |

### Application Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `config.logLevel` | Log level | `info` |
| `config.logFormat` | Log format (json/text) | `json` |
| `config.debug` | Enable debug mode | `false` |
| `config.metricsPath` | Metrics endpoint path | `/metrics` |
| `config.healthPath` | Health endpoint path | `/health` |
| `config.metricsNamespace` | Metrics namespace prefix | `sealos` |

### Leader Election

| Parameter | Description | Default |
|-----------|-------------|---------|
| `config.leaderElection.enabled` | Enable leader election | `true` |
| `config.leaderElection.namespace` | Leader election namespace | `""` (uses release namespace) |
| `config.leaderElection.leaseName` | Lease name | `sealos-state-metric` |
| `config.leaderElection.leaseDuration` | Lease duration | `15s` |
| `config.leaderElection.renewDeadline` | Renew deadline | `10s` |
| `config.leaderElection.retryPeriod` | Retry period | `2s` |

### Collectors

| Parameter | Description | Default |
|-----------|-------------|---------|
| `config.enabledCollectors` | List of enabled collectors | `["domain","node","pod","imagepull","zombie"]` |

#### Domain Collector

| Parameter | Description | Default |
|-----------|-------------|---------|
| `config.domain.enabled` | Enable domain collector | `true` |
| `config.domain.domains` | List of domains to check | `[]` |
| `config.domain.checkTimeout` | HTTP check timeout | `5s` |
| `config.domain.checkInterval` | Check interval | `5m` |
| `config.domain.includeCertCheck` | Include certificate checks | `true` |
| `config.domain.includeHTTPCheck` | Include HTTP checks | `true` |

#### Node Collector

| Parameter | Description | Default |
|-----------|-------------|---------|
| `config.node.enabled` | Enable node collector | `true` |

#### Pod Collector

| Parameter | Description | Default |
|-----------|-------------|---------|
| `config.pod.enabled` | Enable pod collector | `true` |
| `config.pod.restartThreshold` | Restart count threshold | `5` |

#### Image Pull Collector

| Parameter | Description | Default |
|-----------|-------------|---------|
| `config.imagepull.enabled` | Enable image pull collector | `true` |
| `config.imagepull.slowPullThreshold` | Slow pull threshold | `5m` |

#### Zombie Collector

| Parameter | Description | Default |
|-----------|-------------|---------|
| `config.zombie.enabled` | Enable zombie collector | `true` |
| `config.zombie.checkInterval` | Check interval | `30s` |

### Resources

| Parameter | Description | Default |
|-----------|-------------|---------|
| `resources.requests.cpu` | CPU request | `100m` |
| `resources.requests.memory` | Memory request | `128Mi` |
| `resources.limits.cpu` | CPU limit | `1000m` |
| `resources.limits.memory` | Memory limit | `512Mi` |

## Example: Custom Configuration

```yaml
# values-custom.yaml
replicaCount: 2

config:
  logLevel: debug
  domain:
    enabled: true
    domains:
      - example.com
      - test.com
    checkInterval: 10m

  leaderElection:
    enabled: true
    namespace: sealos-system

serviceMonitor:
  enabled: true
  interval: 1m

resources:
  requests:
    cpu: 200m
    memory: 256Mi
  limits:
    cpu: 2000m
    memory: 1Gi
```

Install with custom values:

```bash
helm install sealos-state-metric ./deploy/charts/sealos-state-metric -f values-custom.yaml
```

## Metrics

The following metrics are exposed:

- `sealos_domain_status` - Domain health check status
- `sealos_node_*` - Node metrics
- `sealos_pod_*` - Pod metrics
- `sealos_imagepull_*` - Image pull metrics
- `sealos_zombie_*` - Zombie process metrics

## Troubleshooting

### Check pod status

```bash
kubectl get pods -l app.kubernetes.io/name=sealos-state-metric
```

### View logs

```bash
kubectl logs -l app.kubernetes.io/name=sealos-state-metric -f
```

### Test metrics endpoint

```bash
kubectl port-forward svc/sealos-state-metric 9090:9090
curl http://localhost:9090/metrics
```

### Verify RBAC permissions

```bash
kubectl auth can-i get nodes --as=system:serviceaccount:default:sealos-state-metric
kubectl auth can-i get pods --as=system:serviceaccount:default:sealos-state-metric
```
