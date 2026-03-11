# ImagePull Collector

The `imagepull` collector watches Pod status updates and exposes two kinds of signals:

- image pull failures for containers that are stuck in image-related waiting states
- slow image pulls for containers that stay in `ContainerCreating` longer than the configured threshold

This collector is informer-based and only reads Pod objects. It does not call the CRI directly.

## Configuration

YAML:

```yaml
collectors:
  imagepull:
    slowPullThreshold: "5m"
```

Environment variables:

```bash
COLLECTORS_IMAGEPULL_SLOW_PULL_THRESHOLD=10m
```

Fields:

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `slowPullThreshold` | duration | `5m` | How long a container may remain in `ContainerCreating` before it is reported as a slow pull |

## Metrics

Assuming the global metrics namespace is `sealos`, the collector exports:

### `sealos_image_pull_failures`

Type: `gauge`

Labels:

| Label | Description |
| --- | --- |
| `namespace` | Pod namespace |
| `pod` | Pod name |
| `node` | Node name from `pod.spec.nodeName` |
| `registry` | Parsed registry, for example `docker.io`, `ghcr.io`, `registry.k8s.io` |
| `image` | Container image reference from Pod status |
| `reason` | Classified failure reason |

Value semantics:

- `1` means the container is currently in a tracked image pull failure state
- the series disappears after the container recovers, the Pod is deleted, or the container leaves the failure state

Example:

```promql
sealos_image_pull_failures{
  namespace="default",
  pod="demo",
  node="worker-1",
  registry="docker.io",
  image="docker.io/library/nginx:missing",
  reason="image_not_found"
} 1
```

### `sealos_image_pull_slow`

Type: `gauge`

Labels:

| Label | Description |
| --- | --- |
| `namespace` | Pod namespace |
| `pod` | Pod name |
| `node` | Node name from `pod.spec.nodeName` |
| `registry` | Parsed registry |
| `image` | Container image reference from Pod status |

Value semantics:

- the value is the current slow pull duration in seconds
- it is only exposed after the container has stayed in `ContainerCreating` longer than `slowPullThreshold`
- the series disappears once the container starts, fails image pull, or the Pod is deleted

Example:

```promql
sealos_image_pull_slow{
  namespace="default",
  pod="large-image-job",
  node="worker-2",
  registry="ghcr.io",
  image="ghcr.io/example/big-image:v1"
} 742
```

## Tracked Pod Waiting States

The collector inspects `pod.status.initContainerStatuses[*]` and `pod.status.containerStatuses[*]`.

It tracks these image-related waiting reasons as failures:

| Waiting reason | Source |
| --- | --- |
| `ErrImagePull` | kubelet general image pull failure |
| `ImagePullBackOff` | kubelet is backing off after previous pull failures |
| `RegistryUnavailable` | kubelet propagated CRI registry-unavailable error |
| `InvalidImageName` | kubelet rejected the image reference before pull |
| `ImageInspectError` | kubelet failed while inspecting image metadata |
| `ErrImageNeverPull` | image is absent locally while pull policy forbids pulling |
| `Cancelled` | runtime or kubelet cancelled the pull attempt |

Slow pull tracking uses a different condition:

- waiting reason must be `ContainerCreating`
- `ContainerID` must still be empty
- message must not already indicate `Back-off pulling image`

That last rule prevents the collector from reporting a backoff retry loop as a slow pull.

## Failure Classification

For `ErrImagePull` and `ImagePullBackOff`, the collector inspects kubelet message text and classifies failures into stable categories:

| Classified reason | Typical message patterns |
| --- | --- |
| `image_not_found` | `not found`, `manifest unknown`, `repository does not exist` |
| `proxy_error` | `proxyconnect`, `proxy error` |
| `unauthorized` | `unauthorized`, `failed to authorize`, `authorization failed` |
| `tls_handshake_error` | `tls handshake`, `failed to verify certificate` |
| `io_timeout` | `i/o timeout` |
| `connection_refused` | `connection refused` |
| `network_error` | `failed to do request` |
| `back_off_pulling_image` | kubelet backoff message without a more specific root cause |
| `unknown` | no known pattern matched |

For other kubelet reasons such as `InvalidImageName`, `ImageInspectError`, `ErrImageNeverPull`, and `RegistryUnavailable`, the collector preserves the kubelet reason name in lowercase:

- `invalidimagename`
- `imageinspecterror`
- `errimageneverpull`
- `registryunavailable`
- `cancelled`

## Backoff Handling

Kubelet often changes a container from:

1. `ErrImagePull` with a concrete error message
2. to `ImagePullBackOff` with a generic backoff message

If the node, registry, and image are unchanged, the collector preserves the previous specific classified reason instead of overwriting it with `back_off_pulling_image`.

This avoids losing the real root cause after kubelet enters retry backoff.

## Registry Filtering

The failure metric intentionally only tracks images from registries allowed by the legacy implementation.

Currently included examples:

- short image names, which are treated as Docker Hub
- `docker.io`
- `gcr.io`
- `ghcr.io`
- `k8s.gcr.io`
- `registry.k8s.io`
- `quay.io`
- Alibaba Cloud registries ending in `.aliyuncs.com`
- Sealos Hub style registries

If an image comes from another registry, failure metrics are skipped. Slow pull tracking is not filtered by registry.

## Scope and Limits

- The collector only sees what kubelet has already written into Pod status.
- It does not expose raw CRI error messages as labels, which keeps metric cardinality stable.
- It tracks one failure record per `namespace/pod/container`.
- It tracks both init containers and regular containers.
- The current metric labels do not include container name, even though internal state is container-granular.

## Example Queries

Current failing pulls:

```promql
sealos_image_pull_failures > 0
```

Count failures by classified reason:

```promql
sum by (reason) (sealos_image_pull_failures)
```

Count failures by registry:

```promql
sum by (registry) (sealos_image_pull_failures)
```

Current slow pulls:

```promql
sealos_image_pull_slow > 0
```

Count slow pulls by node:

```promql
count by (node) (sealos_image_pull_slow > 0)
```

Current longest slow pulls:

```promql
topk(10, sealos_image_pull_slow)
```

## Collector Type

| Property | Value |
| --- | --- |
| Type | Informer |
| Watches | Pods |
| Leader election required | Yes, by default collector framework behavior |
