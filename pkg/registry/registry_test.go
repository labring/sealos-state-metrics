//nolint:testpackage
package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

// mockCollector is a simple mock implementation of collector.Collector for testing
type mockCollector struct {
	name          string
	requireLeader bool
	started       bool
	startCount    int
	stopCount     int
	mu            sync.Mutex
	startErr      error
	stopErr       error
}

func (m *mockCollector) Name() string                 { return m.name }
func (m *mockCollector) RequiresLeaderElection() bool { return m.requireLeader }
func (m *mockCollector) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.startErr != nil {
		return m.startErr
	}

	if m.started {
		return fmt.Errorf("collector %s already started", m.name)
	}

	m.started = true
	m.startCount++

	return nil
}

func (m *mockCollector) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stopErr != nil {
		return m.stopErr
	}

	if !m.started {
		return fmt.Errorf("collector %s not started", m.name)
	}

	m.started = false
	m.stopCount++

	return nil
}
func (m *mockCollector) Describe(ch chan<- *prometheus.Desc) {}
func (m *mockCollector) Collect(ch chan<- prometheus.Metric) {}
func (m *mockCollector) Health() error                       { return nil }
func (m *mockCollector) WaitReady(ctx context.Context) error { return nil }

// TestFailedCollectorTracking tests that failed collectors are properly tracked
func TestFailedCollectorTracking(t *testing.T) {
	// Create a new registry for testing
	r := &Registry{
		factories:        make(map[string]collector.Factory),
		collectors:       make(map[string]collector.Collector),
		failedCollectors: make(map[string]error),
	}

	// Register a successful factory
	successFactory := func(ctx *collector.FactoryContext) (collector.Collector, error) {
		return &mockCollector{name: "success"}, nil
	}
	r.factories["success"] = successFactory

	// Register a failing factory
	failFactory := func(ctx *collector.FactoryContext) (collector.Collector, error) {
		return nil, errors.New("mock initialization failure")
	}
	r.factories["fail"] = failFactory

	// Initialize collectors
	cfg := &InitConfig{
		Ctx:                  context.Background(),
		ClientProvider:       nil,
		ConfigContent:        []byte{},
		Identity:             "test",
		NodeName:             "test-node",
		PodName:              "test-pod",
		MetricsNamespace:     "test",
		InformerResyncPeriod: 5 * time.Minute,
		EnabledCollectors:    []string{"success", "fail", "notfound"},
	}

	r.createCollectors(cfg, "Testing")

	// Verify successful collector was created
	if _, exists := r.collectors["success"]; !exists {
		t.Error("Expected 'success' collector to be created")
	}

	// Verify failed collectors were tracked
	failedCollectors := r.GetFailedCollectors()

	if len(failedCollectors) != 2 {
		t.Errorf("Expected 2 failed collectors, got %d", len(failedCollectors))
	}

	// Check 'fail' collector error
	if err, exists := failedCollectors["fail"]; !exists {
		t.Error("Expected 'fail' collector to be in failed collectors")
	} else if err.Error() != "mock initialization failure" {
		t.Errorf("Expected error 'mock initialization failure', got '%s'", err.Error())
	}

	// Check 'notfound' collector error
	if err, exists := failedCollectors["notfound"]; !exists {
		t.Error("Expected 'notfound' collector to be in failed collectors")
	} else if err.Error() != "collector factory not found" {
		t.Errorf("Expected error 'collector factory not found', got '%s'", err.Error())
	}
}

// TestReinitializeClearsFailedCollectors tests that Reinitialize clears failed collectors
func TestReinitializeClearsFailedCollectors(t *testing.T) {
	// Silence logger for cleaner test output
	log.SetLevel(log.ErrorLevel)
	defer log.SetLevel(log.InfoLevel)

	r := &Registry{
		factories:        make(map[string]collector.Factory),
		collectors:       make(map[string]collector.Collector),
		failedCollectors: make(map[string]error),
	}

	// Add a failed collector
	r.failedCollectors["test"] = errors.New("previous error")

	// Register a successful factory
	successFactory := func(ctx *collector.FactoryContext) (collector.Collector, error) {
		return &mockCollector{name: "success"}, nil
	}
	r.factories["success"] = successFactory

	cfg := &InitConfig{
		Ctx:                  context.Background(),
		ClientProvider:       nil,
		ConfigContent:        []byte{},
		Identity:             "test",
		NodeName:             "test-node",
		PodName:              "test-pod",
		MetricsNamespace:     "test",
		InformerResyncPeriod: 5 * time.Minute,
		EnabledCollectors:    []string{"success"},
	}

	// Reinitialize should clear failed collectors
	if err := r.Reinitialize(cfg); err != nil {
		t.Fatalf("Reinitialize failed: %v", err)
	}

	// Verify failed collectors were cleared
	failedCollectors := r.GetFailedCollectors()
	if len(failedCollectors) != 0 {
		t.Errorf("Expected failed collectors to be cleared, got %d", len(failedCollectors))
	}
}

func TestStartStopCollector(t *testing.T) {
	r := &Registry{
		collectors: map[string]collector.Collector{
			"leader": &mockCollector{name: "leader", requireLeader: true},
		},
	}

	if err := r.StartCollector(context.Background(), "leader"); err != nil {
		t.Fatalf("StartCollector failed: %v", err)
	}

	mc := r.collectors["leader"].(*mockCollector)
	if mc.startCount != 1 {
		t.Fatalf("expected start count 1, got %d", mc.startCount)
	}

	if err := r.StopCollector("leader"); err != nil {
		t.Fatalf("StopCollector failed: %v", err)
	}

	if mc.stopCount != 1 {
		t.Fatalf("expected stop count 1, got %d", mc.stopCount)
	}
}

func TestListCollectorsByLeaderRequirement(t *testing.T) {
	r := &Registry{
		collectors: map[string]collector.Collector{
			"domain": &mockCollector{name: "domain", requireLeader: true},
			"lvm":    &mockCollector{name: "lvm", requireLeader: false},
			"node":   &mockCollector{name: "node", requireLeader: true},
		},
	}

	leaderCollectors := r.ListCollectorsByLeaderRequirement(true)
	nonLeaderCollectors := r.ListCollectorsByLeaderRequirement(false)

	if got, want := leaderCollectors, []string{
		"domain",
		"node",
	}; fmt.Sprint(
		got,
	) != fmt.Sprint(
		want,
	) {
		t.Fatalf("expected leader collectors %v, got %v", want, got)
	}

	if got, want := nonLeaderCollectors, []string{"lvm"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("expected non-leader collectors %v, got %v", want, got)
	}
}
