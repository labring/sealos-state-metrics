# Database Collector

The Database collector monitors the connectivity status of databases deployed in Kubernetes clusters using KubeBlocks. It automatically discovers database instances and performs periodic connectivity checks to ensure they are accessible.

## Overview

This collector:
- Automatically discovers databases in Kubernetes namespaces
- Supports MySQL, PostgreSQL, MongoDB, and Redis
- Performs connectivity tests with validation queries
- Exposes connectivity status and response time metrics
- Works with KubeBlocks-managed database clusters

## Supported Databases

### MySQL
- **Secret Pattern**: `{instance-name}-conn-credential`
- **Connection Format**: `mysql://username:password@{instance}-mysql.{namespace}.svc:3306`
- **Validation**: Creates/drops test database and table, performs INSERT/SELECT operations

### PostgreSQL
- **Secret Pattern**: `{instance-name}-conn-credential`
- **Connection Format**: `postgresql://username:password@{instance}-postgresql.{namespace}.svc:5432`
- **Validation**: Lists databases, creates/drops test table, performs INSERT/SELECT operations

### MongoDB
- **Secret Pattern**: `{instance-name}-mongodb-account-root`
- **Connection Format**: `mongodb://username:password@{instance}-mongodb.{namespace}.svc:27017`
- **Validation**: Lists databases, inserts/finds/drops test documents

### Redis
- **Secret Pattern**: `{instance-name}-redis-account-default`
- **Connection Format**: `redis://username:password@{instance}-redis-redis.{namespace}.svc:6379`
- **Validation**: SET/GET/DEL test keys

## Configuration

### Basic Configuration

```yaml
collectors:
  database:
    # Check interval for database connectivity tests (default: 5m)
    checkInterval: "5m"

    # Timeout for each database connection attempt (default: 10s)
    checkTimeout: "10s"

    # List of namespaces to scan for databases (empty = all namespaces)
    namespaces: []
```

### Example: Monitor Specific Namespaces

```yaml
collectors:
  database:
    checkInterval: "3m"
    checkTimeout: "15s"
    namespaces:
      - ns-kkeniczm
      - default
      - production
```

### Environment Variable Overrides

```bash
# Check interval
export COLLECTORS_DATABASE_CHECK_INTERVAL=10m

# Check timeout
export COLLECTORS_DATABASE_CHECK_TIMEOUT=20s

# Namespaces (comma-separated)
export COLLECTORS_DATABASE_NAMESPACES=ns-user1,ns-user2
```

## Metrics

### `sealos_database_connectivity`

Database connectivity status.

- **Type**: Gauge
- **Value**: 1 (connected) or 0 (disconnected)
- **Labels**:
  - `namespace`: Kubernetes namespace
  - `database`: Database instance name
  - `type`: Database type (mysql, postgresql, mongodb, redis)

Example:
```
sealos_database_connectivity{namespace="ns-kkeniczm",database="mysql1",type="mysql"} 1
sealos_database_connectivity{namespace="ns-kkeniczm",database="pg1",type="postgresql"} 1
sealos_database_connectivity{namespace="ns-kkeniczm",database="mongo1",type="mongodb"} 0
sealos_database_connectivity{namespace="ns-kkeniczm",database="redis1",type="redis"} 1
```

### `sealos_database_response_time_seconds`

Database connection response time in seconds.

- **Type**: Gauge
- **Value**: Response time in seconds
- **Labels**:
  - `namespace`: Kubernetes namespace
  - `database`: Database instance name
  - `type`: Database type (mysql, postgresql, mongodb, redis)

Example:
```
sealos_database_response_time_seconds{namespace="ns-kkeniczm",database="mysql1",type="mysql"} 0.152
sealos_database_response_time_seconds{namespace="ns-kkeniczm",database="pg1",type="postgresql"} 0.089
sealos_database_response_time_seconds{namespace="ns-kkeniczm",database="redis1",type="redis"} 0.012
```

## How It Works

### Discovery Process

1. **Namespace Scanning**: The collector scans either all namespaces or a configured list
2. **Secret Discovery**: For each namespace, it looks for secrets with specific labels:
   - MySQL: `app.kubernetes.io/name=apecloud-mysql`
   - PostgreSQL: `app.kubernetes.io/name=postgresql`
   - MongoDB: `apps.kubeblocks.io/component-name=mongodb`
   - Redis: `apps.kubeblocks.io/component-name=redis`
3. **Credential Extraction**: Extracts connection credentials from discovered secrets
4. **Service Resolution**: Constructs service hostnames based on database names

### Connectivity Testing

For each discovered database:
1. Establishes a connection using credentials from the secret
2. Performs database-specific validation queries:
   - **MySQL**: CREATE/USE/DROP database and table operations
   - **PostgreSQL**: List databases, CREATE/DROP table operations
   - **MongoDB**: List databases, INSERT/FIND/DROP document operations
   - **Redis**: SET/GET/DEL key operations
3. Records connection status and response time
4. Cleans up all test data

### Health Checks

The collector performs comprehensive validation:
- Connection establishment
- Authentication verification
- Read/write permissions
- Basic query execution
- Cleanup operations

## Requirements

### Dependencies

The collector requires the following Go libraries:
- `github.com/go-sql-driver/mysql` - MySQL driver
- `github.com/lib/pq` - PostgreSQL driver
- `go.mongodb.org/mongo-driver/mongo` - MongoDB driver
- `github.com/redis/go-redis/v9` - Redis client

### Kubernetes Permissions

The collector needs the following Kubernetes RBAC permissions:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: database-collector
rules:
- apiGroups: [""]
  resources: ["namespaces"]
  verbs: ["list", "get"]
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["list", "get"]
- apiGroups: [""]
  resources: ["services"]
  verbs: ["list", "get"]
```

## Troubleshooting

### Database Not Discovered

**Problem**: Database exists but is not being monitored.

**Solutions**:
1. Check if the secret has the correct name pattern:
   - MySQL/PostgreSQL: `{name}-conn-credential`
   - MongoDB: `{name}-mongodb-account-root`
   - Redis: `{name}-redis-account-default`
2. Verify secret labels match the expected patterns
3. Check if namespace is in the configured `namespaces` list (if specified)
4. Review logs for discovery errors:
   ```bash
   kubectl logs -l app=sealos-state-metrics | grep "database"
   ```

### Connection Failures

**Problem**: Connectivity metric shows 0 (disconnected).

**Solutions**:
1. Check if the database pod is running:
   ```bash
   kubectl get pods -n <namespace> -l app.kubernetes.io/instance=<db-name>
   ```
2. Verify service exists and is accessible:
   ```bash
   kubectl get svc -n <namespace> <db-name>-<type>
   ```
3. Test connectivity from within the cluster:
   ```bash
   kubectl run -it --rm debug --image=busybox --restart=Never -- \
     nc -zv <db-name>-<type>.<namespace>.svc <port>
   ```
4. Check database credentials in the secret:
   ```bash
   kubectl get secret -n <namespace> <secret-name> -o yaml
   ```
5. Review collector logs for detailed error messages:
   ```bash
   kubectl logs -l app=sealos-state-metrics | grep "Failed to"
   ```

### Timeout Issues

**Problem**: Connections timing out.

**Solutions**:
1. Increase `checkTimeout` in configuration:
   ```yaml
   database:
     checkTimeout: "30s"
   ```
2. Check database load and performance
3. Verify network policies allow traffic between collector and databases

### High Response Times

**Problem**: Response times are consistently high.

**Solutions**:
1. Check database resource usage (CPU, memory, disk I/O)
2. Review database logs for slow query warnings
3. Consider increasing database resources
4. Reduce `checkInterval` to avoid overlapping checks

## Best Practices

1. **Namespace Filtering**: For large clusters, specify specific namespaces to reduce overhead:
   ```yaml
   namespaces:
     - production
     - staging
   ```

2. **Check Intervals**: Balance between monitoring frequency and database load:
   - Production: 5-10 minutes
   - Development: 10-15 minutes

3. **Timeouts**: Set reasonable timeouts based on your network:
   - Fast networks: 5-10 seconds
   - Slow networks: 15-30 seconds

4. **Resource Limits**: Ensure the collector has sufficient resources:
   ```yaml
   resources:
     limits:
       cpu: 500m
       memory: 512Mi
     requests:
       cpu: 100m
       memory: 128Mi
   ```

5. **Alert Setup**: Create alerts for connectivity issues:
   ```yaml
   # Example Prometheus alert
   - alert: DatabaseDown
     expr: sealos_database_connectivity == 0
     for: 5m
     annotations:
       summary: "Database {{ $labels.database }} in namespace {{ $labels.namespace }} is down"
   ```

## Security Considerations

1. **Secret Access**: The collector requires read access to database secrets. Ensure RBAC is properly configured.

2. **Network Policies**: If using network policies, allow traffic from the collector to database services.

3. **Read-Only Operations**: The collector creates and drops test resources but does not access production data.

4. **Credential Rotation**: When rotating database credentials, the collector will automatically use new credentials on the next check.

## Performance Impact

- **CPU**: Minimal (~10-50m per check cycle)
- **Memory**: ~50-100MB depending on number of databases
- **Network**: One connection per database per check interval
- **Database Load**: Minimal (simple validation queries that execute in milliseconds)

## Architecture

The collector follows the polling pattern:
1. Runs on a configurable interval (default: 5 minutes)
2. Scans configured namespaces for database secrets
3. Extracts connection information from secrets
4. Performs parallel connectivity checks
5. Updates internal state with results
6. Exposes metrics via Prometheus endpoint

## Related Collectors

- **Node Collector**: Monitors Kubernetes node health
- **Domain Collector**: Monitors external domain health
- **UserBalance Collector**: Monitors user account balances

## Future Enhancements

Potential improvements for future versions:
- Support for additional database types (Elasticsearch, Cassandra, etc.)
- Connection pooling metrics
- Query performance metrics
- Slow query detection
- Database size metrics
- Replication lag metrics
