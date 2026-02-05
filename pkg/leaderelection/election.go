// Package leaderelection manages leader election for high availability.
package leaderelection

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// Config contains the configuration for leader election
type Config struct {
	// Namespace where the lease object will be created
	Namespace string

	// LeaseName is the name of the lease object
	LeaseName string

	// Identity is the unique identifier for this instance (typically pod name)
	Identity string

	// LeaseDuration is the duration that non-leader candidates will wait to force acquire leadership
	LeaseDuration time.Duration

	// RenewDeadline is the duration that the acting leader will retry refreshing leadership before giving up
	RenewDeadline time.Duration

	// RetryPeriod is the duration the LeaderElector clients should wait between tries of actions
	RetryPeriod time.Duration
}

// LeaderElector manages leader election for high availability
type LeaderElector struct {
	config         *Config
	client         kubernetes.Interface
	leaderElection *leaderelection.LeaderElector
	isLeader       atomic.Bool
	currentLeader  atomic.Value
	logger         *log.Entry

	// Callbacks
	onStartedLeading func(ctx context.Context)
	onStoppedLeading func()
	onNewLeader      func(identity string)
}

// NewLeaderElector creates a new LeaderElector instance
func NewLeaderElector(
	cfg *Config,
	client kubernetes.Interface,
	logger *log.Entry,
) (*LeaderElector, error) {
	if cfg == nil {
		return nil, errors.New("config cannot be nil")
	}

	if cfg.Identity == "" {
		return nil, errors.New("identity cannot be empty")
	}

	if client == nil {
		return nil, errors.New("client cannot be nil")
	}

	if logger == nil {
		logger = log.WithField("component", "leader-election")
	}

	logger.WithFields(log.Fields{
		"namespace": cfg.Namespace,
		"leaseName": cfg.LeaseName,
		"identity":  cfg.Identity,
	}).Info("Creating leader elector")

	return &LeaderElector{
		config: cfg,
		logger: logger,
		client: client,
	}, nil
}

// SetCallbacks sets the callback functions for leader election events
func (le *LeaderElector) SetCallbacks(
	onStartedLeading func(ctx context.Context),
	onStoppedLeading func(),
	onNewLeader func(identity string),
) {
	le.onStartedLeading = onStartedLeading
	le.onStoppedLeading = onStoppedLeading
	le.onNewLeader = onNewLeader
}

// Run starts the leader election process and blocks until context is cancelled
func (le *LeaderElector) Run(ctx context.Context) error {
	// Create the resource lock
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      le.config.LeaseName,
			Namespace: le.config.Namespace,
		},
		Client: le.client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: le.config.Identity,
		},
	}

	// Create leader election config
	leConfig := leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   le.config.LeaseDuration,
		RenewDeadline:   le.config.RenewDeadline,
		RetryPeriod:     le.config.RetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				le.isLeader.Store(true)
				le.logger.WithField("identity", le.config.Identity).Info("Started leading")

				if le.onStartedLeading != nil {
					le.onStartedLeading(ctx)
				}
			},
			OnStoppedLeading: func() {
				le.isLeader.Store(false)
				le.logger.WithField("identity", le.config.Identity).Info("Stopped leading")

				if le.onStoppedLeading != nil {
					le.onStoppedLeading()
				}
			},
			OnNewLeader: func(identity string) {
				le.currentLeader.Store(identity)

				if identity == le.config.Identity {
					le.logger.WithField("identity", identity).
						Info("Successfully acquired leadership")
				} else {
					le.logger.WithField("leader", identity).Info("New leader elected")
				}

				if le.onNewLeader != nil {
					le.onNewLeader(identity)
				}
			},
		},
	}

	// Create and run leader elector
	elector, err := leaderelection.NewLeaderElector(leConfig)
	if err != nil {
		return fmt.Errorf("failed to create leader elector: %w", err)
	}

	le.leaderElection = elector

	le.logger.WithFields(log.Fields{
		"leaseDuration": le.config.LeaseDuration,
		"renewDeadline": le.config.RenewDeadline,
		"retryPeriod":   le.config.RetryPeriod,
	}).Info("Starting leader election")

	// Run the leader election (blocks until context is cancelled)
	elector.Run(ctx)

	return nil
}

// IsLeader returns true if this instance is currently the leader
func (le *LeaderElector) IsLeader() bool {
	return le.isLeader.Load()
}

// GetLeader returns the identity of the current leader
func (le *LeaderElector) GetLeader() string {
	if leader := le.currentLeader.Load(); leader != nil {
		if identity, ok := leader.(string); ok {
			return identity
		}
	}

	return ""
}

// GetIdentity returns the identity of this instance
func (le *LeaderElector) GetIdentity() string {
	return le.config.Identity
}
