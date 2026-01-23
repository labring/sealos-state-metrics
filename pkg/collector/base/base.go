package base

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

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
	waitReadyOnCollect     bool          // If true, Collect will wait for collector to be ready
	waitReadyTimeout       time.Duration // Timeout for WaitReady in Collect

	mu        sync.RWMutex
	started   atomic.Bool
	ready     atomic.Bool
	readyCh   chan struct{} // closed when ready, recreated on Start
	stoppedCh chan struct{} // closed when stopped, for WaitReady to detect stop
	//nolint:containedctx // Context is intentionally stored to manage collector lifecycle between Start/Stop
	ctx    context.Context
	cancel context.CancelFunc

	// Metrics registry
	descs []*prometheus.Desc

	// Lifecycle implementation
	lifecycle Lifecycle
}

// BaseCollectorOption is a functional option for configuring BaseCollector
type BaseCollectorOption func(*BaseCollector)

// WithLeaderElection returns an option that sets whether this collector requires leader election.
func WithLeaderElection(required bool) BaseCollectorOption {
	return func(b *BaseCollector) {
		b.requiresLeaderElection = required
	}
}

// WithWaitReadyOnCollect returns an option that sets whether Collect should wait for the collector to be ready.
// If enabled, Collect will call WaitReady with the configured timeout before collecting metrics.
func WithWaitReadyOnCollect(wait bool) BaseCollectorOption {
	return func(b *BaseCollector) {
		b.waitReadyOnCollect = wait
	}
}

// WithWaitReadyTimeout returns an option that sets the timeout for WaitReady in Collect.
// Default is 5 seconds. Only applies if waitReadyOnCollect is enabled.
func WithWaitReadyTimeout(timeout time.Duration) BaseCollectorOption {
	return func(b *BaseCollector) {
		b.waitReadyTimeout = timeout
	}
}

// NewBaseCollector creates a new BaseCollector instance with functional options.
func NewBaseCollector(
	name string,
	collectorType collector.CollectorType,
	logger *log.Entry,
	opts ...BaseCollectorOption,
) *BaseCollector {
	b := &BaseCollector{
		name:                   name,
		collectorType:          collectorType,
		requiresLeaderElection: true, // Default: require leader election
		logger:                 logger,
		waitReadyOnCollect:     false,           // Default: don't wait for ready on collect
		waitReadyTimeout:       5 * time.Second, // Default timeout: 5 seconds
		descs:                  make([]*prometheus.Desc, 0),
	}

	// Apply all options
	for _, opt := range opts {
		opt(b)
	}

	return b
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

// SetLifecycle sets the lifecycle hooks for the collector.
// This allows collectors to add custom start/stop logic without overriding Start()/Stop().
//
// Example:
//
//	c.SetLifecycle(&MyLifecycle{collector: c})
//
// Or using an inline struct:
//
//	c.SetLifecycle(base.LifecycleFuncs{
//	    StartFunc: func(ctx context.Context) error { ... },
//	    StopFunc: func() error { ... },
//	})
func (b *BaseCollector) SetLifecycle(lifecycle Lifecycle) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.lifecycle = lifecycle
}

// Start initializes the collector context
func (b *BaseCollector) Start(ctx context.Context) error {
	if b.started.Load() {
		return fmt.Errorf("collector %s already started", b.name)
	}

	b.mu.Lock()

	if b.started.Load() {
		return fmt.Errorf("collector %s already started", b.name)
	}

	b.ctx, b.cancel = context.WithCancel(ctx)
	b.started.Store(true)
	b.ready.Store(false)
	b.readyCh = make(chan struct{})
	b.stoppedCh = make(chan struct{})
	lifecycle := b.lifecycle
	b.mu.Unlock()

	b.logger.WithFields(log.Fields{
		"name": b.name,
		"type": b.collectorType,
	}).Info("Collector started")

	// Call lifecycle OnStart hook if set
	if lifecycle != nil {
		if err := lifecycle.OnStart(b.ctx); err != nil {
			return fmt.Errorf("collector %s OnStart failed: %w", b.name, err)
		}
	}

	return nil
}

// Stop gracefully stops the collector
func (b *BaseCollector) Stop() error {
	if !b.started.Load() {
		return fmt.Errorf("collector %s not started", b.name)
	}

	b.mu.Lock()

	if !b.started.Load() {
		return fmt.Errorf("collector %s not started", b.name)
	}

	lifecycle := b.lifecycle

	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}

	b.started.Store(false)
	b.ready.Store(false)

	// Close stoppedCh to notify WaitReady callers
	if b.stoppedCh != nil {
		close(b.stoppedCh)
		b.stoppedCh = nil
	}

	b.mu.Unlock()

	// Call lifecycle OnStop hook if set (before cleanup)
	if lifecycle != nil {
		if err := lifecycle.OnStop(); err != nil {
			b.logger.WithError(err).
				WithField("name", b.name).
				Warn("Collector OnStop failed, continuing with cleanup")
		}
	}

	b.logger.WithField("name", b.name).Info("Collector stopped")

	return nil
}

// IsStarted returns whether the collector is started
func (b *BaseCollector) IsStarted() bool {
	return b.started.Load()
}

// SetReady marks the collector as ready to collect metrics
// Note: Once ready, the collector cannot become not-ready again (except through Stop/Start cycle)
func (b *BaseCollector) SetReady() {
	if b.ready.Load() {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Only set ready once per Start cycle
	if b.ready.Load() {
		return
	}

	b.ready.Store(true)
	b.logger.WithField("name", b.name).Debug("Collector marked as ready")

	// Close readyCh to notify WaitReady callers
	if b.readyCh != nil {
		close(b.readyCh)
	}
}

// IsReady returns whether the collector is ready to collect metrics
func (b *BaseCollector) IsReady() bool {
	return b.ready.Load()
}

// WaitReady blocks until the collector is ready to collect metrics
// Returns nil if ready, or an error if context is cancelled or collector is stopped
func (b *BaseCollector) WaitReady(ctx context.Context) error {
	// Fast path: already ready
	if b.ready.Load() {
		return nil
	}

	// Get channels under lock
	b.mu.RLock()
	readyCh := b.readyCh
	stoppedCh := b.stoppedCh
	b.mu.RUnlock()

	// Check if collector was started
	if readyCh == nil || stoppedCh == nil {
		return fmt.Errorf("collector %s not started", b.name)
	}

	select {
	case <-readyCh:
		return nil
	case <-stoppedCh:
		return fmt.Errorf("collector %s stopped before becoming ready", b.name)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Health performs a basic health check
func (b *BaseCollector) Health() error {
	if !b.started.Load() {
		return fmt.Errorf("collector %s not running", b.name)
	}

	b.mu.RLock()
	ctx := b.ctx
	lifecycle := b.lifecycle
	b.mu.RUnlock()

	if ctx == nil {
		return fmt.Errorf("collector %s has nil context", b.name)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("collector %s context cancelled", b.name)
	default:
	}

	// Call lifecycle health check if available
	if lifecycle != nil {
		if err := lifecycle.OnHealth(); err != nil {
			return fmt.Errorf("collector %s health check failed: %w", b.name, err)
		}
	}

	return nil
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

// Collect calls the lifecycle OnCollect hook
func (b *BaseCollector) Collect(ch chan<- prometheus.Metric) {
	// Only collect metrics if the collector has been started
	if !b.started.Load() {
		return
	}

	// If waitReadyOnCollect is enabled, wait for collector to be ready
	if b.waitReadyOnCollect {
		ctx, cancel := context.WithTimeout(context.Background(), b.waitReadyTimeout)
		defer cancel()

		if err := b.WaitReady(ctx); err != nil {
			b.logger.WithError(err).
				WithField("name", b.name).
				Warn("Collector not ready, skipping metric collection")
			return
		}
	} else if !b.ready.Load() {
		b.logger.WithField("name", b.name).
			Warn("Collector not ready, skipping metric collection")
		return
	}

	b.mu.RLock()
	lifecycle := b.lifecycle
	b.mu.RUnlock()

	if lifecycle != nil {
		lifecycle.OnCollect(ch)
	}
}

// MustRegisterDesc registers a descriptor and panics on error
func (b *BaseCollector) MustRegisterDesc(desc *prometheus.Desc) {
	if desc == nil {
		panic(fmt.Sprintf("collector %s: cannot register nil descriptor", b.name))
	}

	b.RegisterDesc(desc)
}
