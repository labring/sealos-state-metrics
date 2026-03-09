package server

import (
	"context"
	"fmt"

	"github.com/labring/sealos-state-metrics/pkg/leaderelection"
)

// setupLeaderElection creates and starts the leader elector
func (s *Server) setupLeaderElection() error {
	// Get Kubernetes client via shared client provider
	client, err := s.getKubernetesClient()
	if err != nil {
		return fmt.Errorf("failed to get Kubernetes client for leader election: %w", err)
	}

	elector, err := leaderelection.NewLeaderElector(
		s.buildLeaderElectionConfig(),
		client,
		s.logger.WithField("subcomponent", "leader-election"),
	)
	if err != nil {
		return fmt.Errorf("failed to create leader elector: %w", err)
	}

	elector.SetCallbacks(
		func(ctx context.Context) {
			s.logger.Info("Became leader, starting leader-required collectors")

			if err := s.registry.StartLeaderCollectors(ctx); err != nil {
				s.logger.WithError(err).Error("Failed to start leader-required collectors")
			}
		},
		func() {
			s.logger.Info("Lost leadership, stopping leader-required collectors")

			if err := s.registry.StopLeaderCollectors(); err != nil {
				s.logger.WithError(err).Error("Failed to stop leader-required collectors")
			}
		},
		func(identity string) {
			s.logger.WithField("leader", identity).Info("New leader elected")
		},
	)

	// Create cancellable context and done channel for cleanup
	s.leMu.Lock()
	defer s.leMu.Unlock()

	leCtx, leCtxCancel := context.WithCancel(s.serverCtx)
	s.leCtxCancel = leCtxCancel
	s.leDoneCh = make(chan struct{})
	s.leaderElector = elector

	go func() {
		defer close(s.leDoneCh)

		s.logger.Info("Starting leader election")

		if err := elector.Run(leCtx); err != nil {
			s.logger.WithError(err).Error("Leader election exited with error")
		}

		s.logger.Info("Leader election stopped")
	}()

	return nil
}

// stopLeaderElection stops the current leader election and releases the lease
func (s *Server) stopLeaderElection() {
	s.leMu.Lock()
	defer s.leMu.Unlock()

	leCtxCancel := s.leCtxCancel
	leDoneCh := s.leDoneCh

	if leCtxCancel != nil {
		s.logger.Info("Stopping leader election and releasing lease")
		leCtxCancel()

		// Wait for leader election goroutine to exit
		if leDoneCh != nil {
			<-leDoneCh
		}

		s.leCtxCancel = nil
		s.leDoneCh = nil
		s.leaderElector = nil
	}
}
