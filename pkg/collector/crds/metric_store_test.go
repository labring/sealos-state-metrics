package crds

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestMetricStoreUpdatesDerivedMetrics(t *testing.T) {
	cfg := &CRDConfig{
		Name: "apps",
		CommonLabels: map[string]string{
			"name":      "metadata.name",
			"namespace": "metadata.namespace",
		},
		Metrics: []MetricConfig{
			{
				Type: "info",
				Name: "app_info",
				Help: "Application information",
				Labels: map[string]string{
					"version": "spec.version",
				},
			},
			{
				Type: "gauge",
				Name: "app_replicas",
				Help: "Application replicas",
				Path: "spec.replicas",
			},
			{
				Type: "count",
				Name: "app_phase_count",
				Help: "Application phase count",
				Path: "status.phase",
			},
			{
				Type: "string_state",
				Name: "app_phase",
				Help: "Application phase",
				Path: "status.phase",
			},
		},
	}

	store, err := newMetricStore(cfg, "", nil)
	if err != nil {
		t.Fatalf("newMetricStore() error = %v", err)
	}

	first := newTestCR("default", "first", "Running", 1)
	second := newTestCR("default", "second", "Pending", 2)
	store.Add(first)
	store.Add(second)

	if got := store.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}

	if got := collectMetricCount(store); got != 8 {
		t.Fatalf("metric count after add = %d, want 8", got)
	}

	assertCountMetric(t, store, "Running", 1)
	assertCountMetric(t, store, "Pending", 1)

	first.Object["status"] = map[string]any{"phase": "Pending"}
	store.Update(first)

	if got := collectMetricCount(store); got != 7 {
		t.Fatalf("metric count after update = %d, want 7", got)
	}

	assertCountMetric(t, store, "Running", 0)
	assertCountMetric(t, store, "Pending", 2)

	store.Delete(second)

	if got := store.Len(); got != 1 {
		t.Fatalf("Len() after delete = %d, want 1", got)
	}

	assertCountMetric(t, store, "Pending", 1)
}

func collectMetricCount(store *metricStore) int {
	ch := make(chan prometheus.Metric, 32)
	store.Collect(ch)
	close(ch)

	count := 0
	for range ch {
		count++
	}

	return count
}

func assertCountMetric(t *testing.T, store *metricStore, phase string, want float64) {
	t.Helper()

	got, found := findCountMetricValue(store, phase)
	if want == 0 {
		if found {
			t.Fatalf("count metric for %q found with value %v, want absent", phase, got)
		}
		return
	}

	if !found {
		t.Fatalf("count metric for %q not found", phase)
	}
	if got != want {
		t.Fatalf("count metric for %q = %v, want %v", phase, got, want)
	}
}

func findCountMetricValue(store *metricStore, phase string) (float64, bool) {
	ch := make(chan prometheus.Metric, 32)
	store.Collect(ch)
	close(ch)

	for metric := range ch {
		dtoMetric := &dto.Metric{}
		if err := metric.Write(dtoMetric); err != nil {
			continue
		}

		if len(dtoMetric.Label) != 1 || dtoMetric.Gauge == nil {
			continue
		}

		if dtoMetric.Label[0].GetName() == defaultValueLabel &&
			dtoMetric.Label[0].GetValue() == phase {
			return dtoMetric.Gauge.GetValue(), true
		}
	}

	return 0, false
}

func newTestCR(namespace, name, phase string, replicas int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]any{
				"replicas": replicas,
				"version":  "v1",
			},
			"status": map[string]any{
				"phase": phase,
			},
		},
	}
}
