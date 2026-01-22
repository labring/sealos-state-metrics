package base

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"github.com/zijiren233/sealos-state-metric/pkg/collector"
)

// BaseCollector provides common functionality for all collectors.
// It implements the basic lifecycle management and state tracking.
type BaseCollector struct {
	name                   string
	collectorType          collector.CollectorType
	requiresLeaderElection bool
	logger                 *log.Entry

	mu        sync.RWMutex
	started   atomic.Bool
	ready     atomic.Bool
	readyCond *sync.Cond
	//nolint:containedctx // Context is intentionally stored to manage collector lifecycle between Start/Stop
	ctx    context.Context
	cancel context.CancelFunc

	// Metrics registry
	descs []*prometheus.Desc

	// Collector-specific collect function
	collectFunc func(ch chan<- prometheus.Metric)
}

// NewBaseCollector creates a new BaseCollector instance.
// By default, collectors require leader election (requiresLeaderElection = true).
func NewBaseCollector(
	name string,
	collectorType collector.CollectorType,
	logger *log.Entry,
) *BaseCollector {
	bc := &BaseCollector{
		name:                   name,
		collectorType:          collectorType,
		requiresLeaderElection: true, // Default: require leader election
		logger:                 logger,
		descs:                  make([]*prometheus.Desc, 0),
	}
	bc.readyCond = sync.NewCond(&bc.mu)
	return bc
}

// NewBaseCollectorWithLeaderElection creates a new BaseCollector with custom leader election requirement
func NewBaseCollectorWithLeaderElection(
	name string,
	collectorType collector.CollectorType,
	requiresLeaderElection bool,
	logger *log.Entry,
) *BaseCollector {
	bc := &BaseCollector{
		name:                   name,
		collectorType:          collectorType,
		requiresLeaderElection: requiresLeaderElection,
		logger:                 logger,
		descs:                  make([]*prometheus.Desc, 0),
	}
	bc.readyCond = sync.NewCond(&bc.mu)
	return bc
}

// Name returns the collector name
func (b *BaseCollector) Name() string {
	return b.name
}

// Type returns the collector type
func (b *BaseCollector) Type() collector.CollectorType {
	return b.collectorType
}

// RequiresLeaderElection returns whether this collector requires leader election
func (b *BaseCollector) RequiresLeaderElection() bool {
	return b.requiresLeaderElection
}

// SetRequiresLeaderElection sets whether this collector requires leader election
func (b *BaseCollector) SetRequiresLeaderElection(requires bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.requiresLeaderElection = requires
}

// Start initializes the collector context
func (b *BaseCollector) Start(ctx context.Context) error {
	if b.started.Load() {
		return fmt.Errorf("collector %s already started", b.name)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.ctx, b.cancel = context.WithCancel(ctx)
	b.started.Store(true)

	b.logger.WithFields(log.Fields{
		"name": b.name,
		"type": b.collectorType,
	}).Info("Collector started")

	return nil
}

// Stop gracefully stops the collector
func (b *BaseCollector) Stop() error {
	if !b.started.Load() {
		return fmt.Errorf("collector %s not started", b.name)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cancel != nil {
		b.cancel()
	}

	b.started.Store(false)
	b.ready.Store(false)

	b.logger.WithField("name", b.name).Info("Collector stopped")

	return nil
}

// IsStarted returns whether the collector is started
func (b *BaseCollector) IsStarted() bool {
	return b.started.Load()
}

// SetReady marks the collector as ready to collect metrics
func (b *BaseCollector) SetReady(ready bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.ready.Store(ready)
	if ready {
		b.logger.WithField("name", b.name).Debug("Collector marked as ready")
		// Broadcast to all waiting goroutines
		b.readyCond.Broadcast()
	}
}

// IsReady returns whether the collector is ready to collect metrics
func (b *BaseCollector) IsReady() bool {
	return b.ready.Load()
}

// WaitReady blocks until the collector is ready to collect metrics
// Returns immediately if already ready, or if context is cancelled
func (b *BaseCollector) WaitReady(ctx context.Context) error {
	// Fast path: already ready
	if b.ready.Load() {
		return nil
	}

	// Wait for ready signal or context cancellation
	done := make(chan struct{})
	go func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		for !b.ready.Load() {
			b.readyCond.Wait()
		}
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Context returns the collector's context
func (b *BaseCollector) Context() context.Context {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.ctx
}

// Health performs a basic health check
func (b *BaseCollector) Health() error {
	if !b.started.Load() {
		return fmt.Errorf("collector %s not running", b.name)
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.ctx == nil {
		return fmt.Errorf("collector %s has nil context", b.name)
	}

	select {
	case <-b.ctx.Done():
		return fmt.Errorf("collector %s context cancelled", b.name)
	default:
		return nil
	}
}

// RegisterDesc registers a prometheus descriptor
func (b *BaseCollector) RegisterDesc(desc *prometheus.Desc) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.descs = append(b.descs, desc)
}

// Describe sends all descriptors to the channel
func (b *BaseCollector) Describe(ch chan<- *prometheus.Desc) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, desc := range b.descs {
		ch <- desc
	}
}

// SetCollectFunc sets the function to be called during metric collection
func (b *BaseCollector) SetCollectFunc(f func(ch chan<- prometheus.Metric)) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.collectFunc = f
}

// Collect calls the collector-specific collect function
func (b *BaseCollector) Collect(ch chan<- prometheus.Metric) {
	// Only collect metrics if the collector has been started
	if !b.started.Load() {
		return
	}

	// Check if collector is ready
	if !b.ready.Load() {
		b.logger.WithField("name", b.name).Warn("Collector not ready, skipping metric collection")
		return
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.collectFunc != nil {
		b.collectFunc(ch)
	}
}

// MustRegisterDesc registers a descriptor and panics on error
func (b *BaseCollector) MustRegisterDesc(desc *prometheus.Desc) {
	if desc == nil {
		panic(fmt.Sprintf("collector %s: cannot register nil descriptor", b.name))
	}

	b.RegisterDesc(desc)
}
