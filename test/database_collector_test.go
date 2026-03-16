package test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	dbcollector "github.com/labring/sealos-state-metrics/pkg/collector/database"
	"github.com/labring/sealos-state-metrics/pkg/config"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

type fakeClientProvider struct {
	client kubernetes.Interface
}

func (p *fakeClientProvider) GetRestConfig() (*rest.Config, error) {
	return &rest.Config{}, nil
}

func (p *fakeClientProvider) GetClient() (kubernetes.Interface, error) {
	return p.client, nil
}

func TestDatabaseCollectorExportsUnavailableMetricsForAllDatabaseTypes(t *testing.T) {
	t.Parallel()

	client := k8sfake.NewSimpleClientset(
		newService("default", "mysql-demo"),
		newReadyEndpoints("default", "mysql-demo"),
		newMySQLSecret("default", "mysql-demo"),
		newService("default", "pg-demo"),
		newReadyEndpoints("default", "pg-demo"),
		newPostgreSQLSecret("default", "pg-demo"),
		newService("default", "mongo-demo-mongodb"),
		newReadyEndpoints("default", "mongo-demo-mongodb"),
		newMongoDBSecret("default", "mongo-demo"),
		newService("default", "redis-demo-redis-redis"),
		newReadyEndpoints("default", "redis-demo-redis-redis"),
		newRedisSecret("default", "redis-demo"),
	)

	collector := newDatabaseCollector(t, client, map[string]string{
		"COLLECTORS_DATABASE_NAMESPACES":     "default",
		"COLLECTORS_DATABASE_CHECK_TIMEOUT":  "2s",
		"COLLECTORS_DATABASE_CHECK_INTERVAL": "1h",
	})

	startCollector(t, collector)

	families := gatherMetricFamilies(t, collector)

	assertGaugeValue(t, families, "sealos_database_connectivity", map[string]string{
		"namespace": "default",
		"database":  "mysql-demo",
		"type":      "mysql",
	}, 0)
	assertGaugeValue(t, families, "sealos_database_connectivity", map[string]string{
		"namespace": "default",
		"database":  "pg-demo",
		"type":      "postgresql",
	}, 0)
	assertGaugeValue(t, families, "sealos_database_connectivity", map[string]string{
		"namespace": "default",
		"database":  "mongo-demo",
		"type":      "mongodb",
	}, 0)
	assertGaugeValue(t, families, "sealos_database_connectivity", map[string]string{
		"namespace": "default",
		"database":  "redis-demo",
		"type":      "redis",
	}, 0)

	assertMetricCount(t, families, "sealos_database_connectivity", 4)
	assertMetricCount(t, families, "sealos_database_response_time_seconds", 4)

	assertGaugeValue(t, families, "sealos_database_total", map[string]string{"type": "mysql"}, 1)
	assertGaugeValue(t, families, "sealos_database_total", map[string]string{"type": "postgresql"}, 1)
	assertGaugeValue(t, families, "sealos_database_total", map[string]string{"type": "mongodb"}, 1)
	assertGaugeValue(t, families, "sealos_database_total", map[string]string{"type": "redis"}, 1)
	assertGaugeValue(t, families, "sealos_database_total", map[string]string{"type": "all"}, 4)

	assertGaugeValue(t, families, "sealos_database_available", map[string]string{"type": "mysql"}, 0)
	assertGaugeValue(t, families, "sealos_database_available", map[string]string{"type": "postgresql"}, 0)
	assertGaugeValue(t, families, "sealos_database_available", map[string]string{"type": "mongodb"}, 0)
	assertGaugeValue(t, families, "sealos_database_available", map[string]string{"type": "redis"}, 0)
	assertGaugeValue(t, families, "sealos_database_available", map[string]string{"type": "all"}, 0)

	assertGaugeValue(t, families, "sealos_database_unavailable", map[string]string{"type": "mysql"}, 1)
	assertGaugeValue(t, families, "sealos_database_unavailable", map[string]string{"type": "postgresql"}, 1)
	assertGaugeValue(t, families, "sealos_database_unavailable", map[string]string{"type": "mongodb"}, 1)
	assertGaugeValue(t, families, "sealos_database_unavailable", map[string]string{"type": "redis"}, 1)
	assertGaugeValue(t, families, "sealos_database_unavailable", map[string]string{"type": "all"}, 4)
}

func TestDatabaseCollectorFiltersNamespaceAndCredentialSecrets(t *testing.T) {
	t.Parallel()

	client := k8sfake.NewSimpleClientset(
		newService("default", "mysql-demo"),
		newReadyEndpoints("default", "mysql-demo"),
		newMySQLSecret("default", "mysql-demo"),
		newInvalidMySQLCredentialSecret("default", "mysql-demo"),
		newService("other", "pg-demo"),
		newReadyEndpoints("other", "pg-demo"),
		newPostgreSQLSecret("other", "pg-demo"),
	)

	collector := newDatabaseCollector(t, client, map[string]string{
		"COLLECTORS_DATABASE_NAMESPACES":     "default",
		"COLLECTORS_DATABASE_CHECK_TIMEOUT":  "2s",
		"COLLECTORS_DATABASE_CHECK_INTERVAL": "1h",
	})

	startCollector(t, collector)

	families := gatherMetricFamilies(t, collector)

	assertMetricCount(t, families, "sealos_database_connectivity", 1)
	assertGaugeValue(t, families, "sealos_database_connectivity", map[string]string{
		"namespace": "default",
		"database":  "mysql-demo",
		"type":      "mysql",
	}, 0)

	assertGaugeValue(t, families, "sealos_database_total", map[string]string{"type": "mysql"}, 1)
	assertGaugeValue(t, families, "sealos_database_total", map[string]string{"type": "all"}, 1)
	assertNoMetricWithLabels(t, families, "sealos_database_connectivity", map[string]string{
		"namespace": "other",
		"database":  "pg-demo",
		"type":      "postgresql",
	})
}

func newDatabaseCollector(
	t *testing.T,
	client kubernetes.Interface,
	environment map[string]string,
) *dbcollector.Collector {
	t.Helper()

	logger := log.New()
	logger.SetOutput(io.Discard)
	logger.SetLevel(log.DebugLevel)

	configLoader := config.NewWrapConfigLoader()
	configLoader.Add(config.NewEnvConfigLoader(config.WithEnvironment(environment)))

	created, err := dbcollector.NewCollector(&collector.FactoryContext{
		Ctx:              context.Background(),
		ConfigLoader:     configLoader,
		ClientProvider:   &fakeClientProvider{client: client},
		MetricsNamespace: "sealos",
		Logger:           log.NewEntry(logger),
	})
	if err != nil {
		t.Fatalf("create database collector: %v", err)
	}

	typed, ok := created.(*dbcollector.Collector)
	if !ok {
		t.Fatalf("unexpected collector type %T", created)
	}

	return typed
}

func startCollector(t *testing.T, c *dbcollector.Collector) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		_ = c.Stop()
		cancel()
	})

	if err := c.Start(ctx); err != nil {
		t.Fatalf("start collector: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()

	if err := c.WaitReady(waitCtx); err != nil {
		t.Fatalf("wait for collector ready: %v", err)
	}
}

func gatherMetricFamilies(t *testing.T, c prometheus.Collector) map[string]*dto.MetricFamily {
	t.Helper()

	registry := prometheus.NewRegistry()
	if err := registry.Register(c); err != nil {
		t.Fatalf("register collector: %v", err)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	result := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		result[family.GetName()] = family
	}

	return result
}

func assertMetricCount(
	t *testing.T,
	families map[string]*dto.MetricFamily,
	name string,
	want int,
) {
	t.Helper()

	family, ok := families[name]
	if !ok {
		t.Fatalf("metric family %q not found", name)
	}

	if got := len(family.Metric); got != want {
		t.Fatalf("metric family %q count = %d, want %d", name, got, want)
	}
}

func assertGaugeValue(
	t *testing.T,
	families map[string]*dto.MetricFamily,
	name string,
	labels map[string]string,
	want float64,
) {
	t.Helper()

	metric := findMetric(t, families, name, labels)
	if metric.Gauge == nil {
		t.Fatalf("metric %q with labels %v is not a gauge", name, labels)
	}

	if got := metric.Gauge.GetValue(); got != want {
		t.Fatalf("metric %q with labels %v = %v, want %v", name, labels, got, want)
	}
}

func assertNoMetricWithLabels(
	t *testing.T,
	families map[string]*dto.MetricFamily,
	name string,
	labels map[string]string,
) {
	t.Helper()

	family, ok := families[name]
	if !ok {
		return
	}

	for _, metric := range family.Metric {
		if hasLabels(metric, labels) {
			t.Fatalf("metric %q unexpectedly found with labels %v", name, labels)
		}
	}
}

func findMetric(
	t *testing.T,
	families map[string]*dto.MetricFamily,
	name string,
	labels map[string]string,
) *dto.Metric {
	t.Helper()

	family, ok := families[name]
	if !ok {
		t.Fatalf("metric family %q not found", name)
	}

	for _, metric := range family.Metric {
		if hasLabels(metric, labels) {
			return metric
		}
	}

	t.Fatalf("metric %q with labels %v not found", name, labels)
	return nil
}

func hasLabels(metric *dto.Metric, labels map[string]string) bool {
	if len(metric.Label) != len(labels) {
		return false
	}

	for key, want := range labels {
		found := false
		for _, label := range metric.Label {
			if label.GetName() == key && label.GetValue() == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

func newService(namespace, name string) runtime.Object {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func newReadyEndpoints(namespace, name string) runtime.Object {
	return &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.10"},
				},
				Ports: []corev1.EndpointPort{
					{Port: 80},
				},
			},
		},
	}
}

func newMySQLSecret(namespace, name string) runtime.Object {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name + "-conn-credential",
			Labels: map[string]string{
				"apps.kubeblocks.io/cluster-type": "mysql",
			},
		},
		Data: map[string][]byte{
			"username": []byte("root"),
			"password": []byte("secret"),
			"endpoint": []byte(name + ":3306"),
			"host":     []byte(name + "." + namespace + ".svc.cluster.local"),
		},
	}
}

func newInvalidMySQLCredentialSecret(namespace, name string) runtime.Object {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name + "-ignored",
			Labels: map[string]string{
				"apps.kubeblocks.io/cluster-type": "mysql",
			},
		},
		Data: map[string][]byte{
			"username": []byte("root"),
			"password": []byte("secret"),
			"endpoint": []byte(name + ":3306"),
			"host":     []byte(name + "." + namespace + ".svc.cluster.local"),
		},
	}
}

func newPostgreSQLSecret(namespace, name string) runtime.Object {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name + "-conn-credential",
			Labels: map[string]string{
				"app.kubernetes.io/name": "postgresql",
			},
		},
		Data: map[string][]byte{
			"username": []byte("postgres"),
			"password": []byte("secret"),
			"host":     []byte(name + "." + namespace + ".svc.cluster.local"),
			"port":     []byte("5432"),
		},
	}
}

func newMongoDBSecret(namespace, name string) runtime.Object {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name + "-mongodb-account-root",
			Labels: map[string]string{
				"apps.kubeblocks.io/component-name": "mongodb",
			},
		},
		Data: map[string][]byte{
			"username": []byte("root"),
			"password": []byte("secret"),
			"host":     []byte(name + "-mongodb." + namespace + ".svc.cluster.local"),
		},
	}
}

func newRedisSecret(namespace, name string) runtime.Object {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name + "-redis-account-default",
			Labels: map[string]string{
				"apps.kubeblocks.io/component-name": "redis",
			},
		},
		Data: map[string][]byte{
			"password": []byte("secret"),
			"host":     []byte(name + "-redis-redis." + namespace + ".svc.cluster.local"),
		},
	}
}
