package base

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

// Lifecycle defines hooks for collector lifecycle events
type Lifecycle interface {
	OnStart(ctx context.Context) error
	OnStop() error
	OnCollect(ch chan<- prometheus.Metric)
}

var _ Lifecycle = (*LifecycleFuncs)(nil)

// LifecycleFuncs is a helper struct that implements Lifecycle interface with function fields
type LifecycleFuncs struct {
	StartFunc   func(ctx context.Context) error
	StopFunc    func() error
	CollectFunc func(ch chan<- prometheus.Metric)
}

// OnStart calls StartFunc if set
func (lf LifecycleFuncs) OnStart(ctx context.Context) error {
	if lf.StartFunc != nil {
		return lf.StartFunc(ctx)
	}
	return nil
}

// OnStop calls StopFunc if set
func (lf LifecycleFuncs) OnStop() error {
	if lf.StopFunc != nil {
		return lf.StopFunc()
	}
	return nil
}

// OnCollect calls CollectFunc if set
func (lf LifecycleFuncs) OnCollect(ch chan<- prometheus.Metric) {
	if lf.CollectFunc != nil {
		lf.CollectFunc(ch)
	}
}
