# CRDs Collector

The CRDs (Custom Resource Definitions) collector enables dynamic monitoring of any Kubernetes Custom Resource without code modifications. This is one of the most powerful features of Sealos State Metrics, allowing you to monitor application-specific resources through simple configuration changes.

## Overview

Unlike kube-state-metrics which requires recompilation to add CRD support, the CRDs collector uses Kubernetes Dynamic Client to watch any custom resource at runtime. You define what to monitor, which fields to extract, and what metrics to generate—all through YAML configuration.

### Key Features

- **Zero Code Changes**: Add monitoring for new CRDs by editing configuration
- **Hot Reload Support**: Update monitored CRDs without restarting pods
- **Rich Metric Types**: 7 different metric types to cover various use cases
- **Flexible Field Extraction**: Use JSONPath-like syntax to access nested fields
- **Label Control**: Define which labels to include for each metric
- **Namespace Filtering**: Monitor CRDs in specific namespaces or cluster-wide

## Configuration

### Basic Example

```yaml
collectors:
  crds:
    crds:
      - name: "my-application"
        gvr:
          group: "apps.example.com"
          version: "v1"
          resource: "applications"
        namespaces: ["default", "production"]  # Empty = all namespaces

        # Common labels applied to all metrics
        commonLabels:
          namespace: "metadata.namespace"
          name: "metadata.name"
          app_version: "spec.version"

        # Define metrics to generate
        metrics:
          - type: "info"
            name: "application_info"
            help: "Information about the application"
            labels:
              team: "metadata.labels.team"
              environment: "metadata.labels.environment"

          - type: "gauge"
            name: "application_replicas"
            help: "Number of application replicas"
            path: "spec.replicas"

          - type: "conditions"
            name: "application_condition"
            help: "Application status conditions"
            path: "status.conditions"
```

### Configuration Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Unique identifier for this CRD config |
| `gvr` | object | Yes | Group/Version/Resource identifier |
| `gvr.group` | string | Yes | API group (empty for core resources) |
| `gvr.version` | string | Yes | API version (e.g., `v1`, `v1beta1`) |
| `gvr.resource` | string | Yes | Resource plural name |
| `namespaces` | []string | No | Namespaces to watch (empty = all) |
| `commonLabels` | map | No | Labels added to all metrics |
| `metrics` | []Metric | Yes | Metric definitions |

### GVR (Group/Version/Resource)

The GVR identifies which Custom Resource to monitor:

```yaml
# Example: monitoring Deployments (built-in resource)
gvr:
  group: "apps"
  version: "v1"
  resource: "deployments"

# Example: monitoring a custom resource
gvr:
  group: "stable.example.com"
  version: "v1"
  resource: "crontabs"
```

**How to find GVR:**
```bash
# List all API resources
kubectl api-resources

# Find specific CRD
kubectl get crd myresource.example.com -o yaml
# Look for: spec.group, spec.versions[].name, spec.names.plural
```

### Common Labels

Common labels are extracted once and applied to all metrics for this CRD:

```yaml
commonLabels:
  namespace: "metadata.namespace"        # Always recommended
  name: "metadata.name"                 # Always recommended
  uid: "metadata.uid"                   # Useful for tracking across recreations
  cluster: "metadata.labels.cluster"    # Multi-cluster scenarios
  owner: "metadata.labels.owner"        # Ownership tracking
```

**Best Practices:**
- Always include `namespace` and `name` for filtering
- Avoid high-cardinality labels (e.g., timestamps, IDs)
- Use labels that are stable across resource updates

## Metric Types

### 1. Info Metric

Exposes metadata as labels with a constant value of 1.

```yaml
- type: "info"
  name: "app_info"
  help: "Application metadata"
  labels:
    version: "spec.version"
    team: "metadata.labels.team"
    repository: "spec.repository"
```

**Generated Metric:**
```promql
sealos_app_info{namespace="default",name="myapp",version="v1.0.0",team="platform",repository="github.com/org/app"} 1
```

**Use Cases:**
- Expose version information
- Track team ownership
- Record deployment metadata

### 2. Gauge Metric

Extracts a numeric value from the resource.

```yaml
- type: "gauge"
  name: "app_replicas"
  help: "Number of replicas"
  path: "spec.replicas"
```

**Generated Metric:**
```promql
sealos_app_replicas{namespace="default",name="myapp"} 3
```

**Supported Types:**
- Integers: `spec.replicas`, `status.readyReplicas`
- Floats: `status.cpuUsage`, `spec.threshold`
- Booleans: Converted to 0 (false) or 1 (true)

**Use Cases:**
- Monitor replica counts
- Track resource quotas
- Export numerical status fields

### 3. Count Metric

Aggregates resources by a field value.

```yaml
- type: "count"
  name: "app_count"
  help: "Number of applications by status"
  path: "status.phase"
```

**Generated Metrics:**
```promql
sealos_app_count{status="Running"} 5
sealos_app_count{status="Pending"} 2
sealos_app_count{status="Failed"} 1
```

**Use Cases:**
- Count resources by phase/state
- Track distribution by category
- Monitor status breakdowns

### 4. String State Metric

Exposes the current state as a label.

```yaml
- type: "string_state"
  name: "app_state"
  help: "Current application state"
  path: "status.phase"
```

**Generated Metric:**
```promql
sealos_app_state{namespace="default",name="myapp",state="Running"} 1
```

**Use Cases:**
- Monitor current phase/state
- Track lifecycle stages
- Alert on specific states

### 5. Map State Metric

Creates metrics for each entry in a map with state as label.

```yaml
- type: "map_state"
  name: "app_feature"
  help: "Application feature flags"
  path: "spec.features"
```

**Resource Example:**
```yaml
spec:
  features:
    caching: "enabled"
    logging: "disabled"
    monitoring: "enabled"
```

**Generated Metrics:**
```promql
sealos_app_feature{namespace="default",name="myapp",key="caching",value="enabled"} 1
sealos_app_feature{namespace="default",name="myapp",key="logging",value="disabled"} 1
sealos_app_feature{namespace="default",name="myapp",key="monitoring",value="enabled"} 1
```

**Use Cases:**
- Monitor feature flags
- Track configuration states
- Export key-value pairs

### 6. Map Gauge Metric

Extracts numeric values from a map.

```yaml
- type: "map_gauge"
  name: "app_resource_usage"
  help: "Resource usage by type"
  path: "status.resourceUsage"
```

**Resource Example:**
```yaml
status:
  resourceUsage:
    cpu: 0.5
    memory: 1024
    disk: 5000
```

**Generated Metrics:**
```promql
sealos_app_resource_usage{namespace="default",name="myapp",key="cpu"} 0.5
sealos_app_resource_usage{namespace="default",name="myapp",key="memory"} 1024
sealos_app_resource_usage{namespace="default",name="myapp",key="disk"} 5000
```

**Use Cases:**
- Monitor multiple resource types
- Track per-component metrics
- Export structured numeric data

### 7. Conditions Metric

Processes Kubernetes-style condition arrays.

```yaml
- type: "conditions"
  name: "app_condition"
  help: "Application conditions"
  path: "status.conditions"
```

**Resource Example:**
```yaml
status:
  conditions:
    - type: "Ready"
      status: "True"
      lastTransitionTime: "2024-01-01T00:00:00Z"
    - type: "Progressing"
      status: "False"
      lastTransitionTime: "2024-01-01T00:00:00Z"
```

**Generated Metrics:**
```promql
sealos_app_condition{namespace="default",name="myapp",type="Ready",status="True"} 1
sealos_app_condition{namespace="default",name="myapp",type="Progressing",status="False"} 1
```

**Use Cases:**
- Monitor resource readiness
- Track condition transitions
- Alert on unhealthy conditions

## Complete Examples

### Example 1: Monitoring Database Clusters

```yaml
collectors:
  crds:
    crds:
      - name: "postgresql-clusters"
        gvr:
          group: "postgresql.cnpg.io"
          version: "v1"
          resource: "clusters"
        namespaces: []  # All namespaces

        commonLabels:
          namespace: "metadata.namespace"
          cluster: "metadata.name"

        metrics:
          # Basic info
          - type: "info"
            name: "postgres_cluster_info"
            help: "PostgreSQL cluster information"
            labels:
              postgres_version: "spec.imageName"
              storage_class: "spec.storage.storageClass"

          # Instance count
          - type: "gauge"
            name: "postgres_cluster_instances"
            help: "Number of PostgreSQL instances"
            path: "spec.instances"

          # Ready replicas
          - type: "gauge"
            name: "postgres_cluster_ready_instances"
            help: "Number of ready instances"
            path: "status.readyInstances"

          # Cluster phase
          - type: "string_state"
            name: "postgres_cluster_phase"
            help: "Current cluster phase"
            path: "status.phase"

          # Conditions
          - type: "conditions"
            name: "postgres_cluster_condition"
            help: "Cluster health conditions"
            path: "status.conditions"
```

**Queries:**
```promql
# Clusters with less than desired instances
postgres_cluster_ready_instances < postgres_cluster_instances

# Unhealthy clusters
postgres_cluster_condition{type="Ready",status!="True"} == 1

# Clusters by PostgreSQL version
count by (postgres_version) (postgres_cluster_info)
```

### Example 2: Monitoring Application Deployments

```yaml
collectors:
  crds:
    crds:
      - name: "app-deployments"
        gvr:
          group: "app.example.com"
          version: "v1alpha1"
          resource: "appdeployments"

        commonLabels:
          namespace: "metadata.namespace"
          app: "metadata.name"
          team: "metadata.labels.team"

        metrics:
          # Deployment info
          - type: "info"
            name: "app_deployment_info"
            help: "Application deployment metadata"
            labels:
              image: "spec.image"
              version: "spec.version"
              environment: "spec.environment"

          # Resource limits
          - type: "gauge"
            name: "app_deployment_cpu_limit"
            help: "CPU limit in cores"
            path: "spec.resources.limits.cpu"

          - type: "gauge"
            name: "app_deployment_memory_limit"
            help: "Memory limit in bytes"
            path: "spec.resources.limits.memory"

          # Feature flags
          - type: "map_state"
            name: "app_deployment_feature"
            help: "Feature flag states"
            path: "spec.features"

          # Health metrics
          - type: "map_gauge"
            name: "app_deployment_health_score"
            help: "Health scores by component"
            path: "status.healthScores"

          # Deployment status
          - type: "string_state"
            name: "app_deployment_status"
            help: "Current deployment status"
            path: "status.state"
```

### Example 3: Monitoring Custom Ingress Resources

```yaml
collectors:
  crds:
    crds:
      - name: "custom-ingress"
        gvr:
          group: "networking.example.com"
          version: "v1"
          resource: "customingresses"

        commonLabels:
          namespace: "metadata.namespace"
          ingress: "metadata.name"

        metrics:
          # Ingress info
          - type: "info"
            name: "custom_ingress_info"
            help: "Custom ingress metadata"
            labels:
              host: "spec.host"
              tls_enabled: "spec.tls.enabled"
              cert_issuer: "spec.tls.certIssuer"

          # Backend count
          - type: "gauge"
            name: "custom_ingress_backends"
            help: "Number of configured backends"
            path: "spec.backendCount"

          # Certificate expiry
          - type: "gauge"
            name: "custom_ingress_cert_expiry_seconds"
            help: "Certificate expiry in seconds"
            path: "status.certificateExpirySeconds"

          # Traffic stats
          - type: "map_gauge"
            name: "custom_ingress_traffic"
            help: "Traffic statistics"
            path: "status.traffic"

          # Ingress conditions
          - type: "conditions"
            name: "custom_ingress_condition"
            help: "Ingress status conditions"
            path: "status.conditions"
```

## Field Path Syntax

The `path` and `labels` fields use a JSONPath-like syntax:

| Pattern | Example | Description |
|---------|---------|-------------|
| Simple field | `spec.replicas` | Access top-level field |
| Nested field | `spec.template.image` | Dot notation for nesting |
| Label | `metadata.labels.app` | Access map values |
| Annotation | `metadata.annotations.version` | Access annotations |
| Array element | `status.conditions[0].type` | Access array by index (rarely used) |

**Supported Root Paths:**
- `metadata.*`: Resource metadata (name, namespace, labels, annotations, etc.)
- `spec.*`: Resource specification
- `status.*`: Resource status

## Advanced Usage

### Filtering by Namespace

Monitor CRDs in specific namespaces only:

```yaml
- name: "production-apps"
  gvr:
    group: "apps.example.com"
    version: "v1"
    resource: "applications"
  namespaces:
    - "production"
    - "staging"
```

### Multiple Configurations for Same CRD

Monitor the same CRD with different metrics in different namespaces:

```yaml
crds:
  # Production monitoring (detailed)
  - name: "apps-production"
    gvr:
      group: "apps.example.com"
      version: "v1"
      resource: "applications"
    namespaces: ["production"]
    metrics:
      # ... detailed metrics

  # Development monitoring (basic)
  - name: "apps-development"
    gvr:
      group: "apps.example.com"
      version: "v1"
      resource: "applications"
    namespaces: ["development"]
    metrics:
      # ... basic metrics only
```

### Conditional Metrics

Use PromQL to create conditional metrics:

```yaml
# Define base metrics
- type: "gauge"
  name: "app_desired_replicas"
  path: "spec.replicas"

- type: "gauge"
  name: "app_ready_replicas"
  path: "status.readyReplicas"
```

**PromQL Queries:**
```promql
# Alert on replica mismatch
(app_desired_replicas - app_ready_replicas) > 0

# Calculate availability percentage
(app_ready_replicas / app_desired_replicas) * 100
```

## Troubleshooting

### CRD Not Discovered

**Problem:** Metrics not appearing for your CRD.

**Solutions:**

1. Verify CRD exists:
```bash
kubectl get crd myresource.example.com
```

2. Check GVR configuration:
```bash
kubectl api-resources | grep myresource
# Compare group, version, resource with your config
```

3. Verify RBAC permissions:
```bash
kubectl auth can-i list myresources.example.com --as=system:serviceaccount:monitoring:sealos-state-metrics
```

4. Check collector logs:
```bash
kubectl logs -l app=sealos-state-metrics | grep "crds"
```

### Field Not Found

**Problem:** Metric shows 0 or is missing.

**Solutions:**

1. Verify field exists in resource:
```bash
kubectl get myresource example -o yaml
# Check if the path exists
```

2. Test field path:
```bash
kubectl get myresource example -o jsonpath='{.spec.replicas}'
```

3. Check for typos in path:
```yaml
# Correct
path: "spec.replicas"

# Incorrect
path: "spec.replica"   # Missing 's'
path: ".spec.replicas" # Extra leading dot
```

### High Cardinality

**Problem:** Too many time series generated.

**Solutions:**

1. Reduce common labels:
```yaml
# Before: uid creates one series per resource
commonLabels:
  namespace: "metadata.namespace"
  name: "metadata.name"
  uid: "metadata.uid"  # Remove this

# After
commonLabels:
  namespace: "metadata.namespace"
  name: "metadata.name"
```

2. Use count metrics instead of per-resource metrics:
```yaml
# Instead of gauge per resource
- type: "gauge"
  name: "app_status"
  path: "status.phase"

# Use count aggregation
- type: "count"
  name: "app_count"
  path: "status.phase"
```

3. Filter namespaces:
```yaml
# Monitor only important namespaces
namespaces:
  - "production"
  - "staging"
```

## Best Practices

### 1. Start Small

Begin with basic metrics and expand:

```yaml
# Phase 1: Basic info
metrics:
  - type: "info"
    name: "resource_info"
    labels:
      version: "spec.version"

# Phase 2: Add key gauges
  - type: "gauge"
    name: "resource_replicas"
    path: "spec.replicas"

# Phase 3: Add conditions
  - type: "conditions"
    name: "resource_condition"
    path: "status.conditions"
```

### 2. Consistent Naming

Follow Prometheus naming conventions:

```yaml
# Good
- name: "app_replicas_total"           # Clear, with unit
- name: "app_memory_bytes"             # Explicit unit
- name: "app_cpu_cores"                # Clear unit

# Bad
- name: "replicas"                     # Too generic
- name: "memory"                       # Missing unit
- name: "app-cpu-usage"                # Use underscores
```

### 3. Meaningful Help Text

```yaml
# Good
help: "Number of desired replicas for the application"

# Bad
help: "Replicas"
```

### 4. Label Cardinality Control

```yaml
# Good: Stable, low-cardinality labels
commonLabels:
  namespace: "metadata.namespace"
  name: "metadata.name"
  environment: "metadata.labels.environment"  # Limited values

# Bad: High-cardinality labels
commonLabels:
  uid: "metadata.uid"                   # Unique per resource
  creation_timestamp: "metadata.creationTimestamp"  # Always unique
  pod_ip: "status.podIP"                # Changes frequently
```

### 5. Test in Development First

```yaml
# Test in dev namespace first
- name: "test-crd"
  gvr:
    group: "example.com"
    version: "v1"
    resource: "testresources"
  namespaces: ["development"]
  # ... metrics

# After validation, expand to production
  namespaces: ["development", "staging", "production"]
```

## Performance Considerations

### Memory Usage

Each monitored CRD creates an informer with an in-memory cache:

- **Small CRDs** (< 100 resources): ~10-50 MB per CRD
- **Medium CRDs** (100-1000 resources): ~50-200 MB per CRD
- **Large CRDs** (> 1000 resources): ~200+ MB per CRD

**Optimization:**

1. Use transforms to reduce cached data (not yet implemented)
2. Monitor only necessary namespaces
3. Limit number of monitored CRDs

### CPU Usage

CPU usage is proportional to:
- Number of monitored CRDs
- Update frequency of resources
- Complexity of metric extraction

**Typical Usage:**
- 5 CRDs with moderate activity: ~10-50m CPU
- 20 CRDs with high activity: ~50-200m CPU

## Collector Type

**Type:** Informer
**Leader Election Required:** Yes

The CRDs collector uses Kubernetes Dynamic Client and Dynamic Informers to watch custom resources in real-time. Leader election ensures only one instance processes events to avoid duplicate metrics.

## Related Resources

- [Kubernetes Custom Resources](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/)
- [client-go Dynamic Client](https://github.com/kubernetes/client-go/tree/master/dynamic)
- [JSONPath Syntax](https://kubernetes.io/docs/reference/kubectl/jsonpath/)

## Future Enhancements

Planned improvements:
- [ ] Transform support for memory optimization
- [ ] Regex-based field filtering
- [ ] Array iteration for list metrics
- [ ] Custom label transformations
- [ ] Metric aggregation rules
