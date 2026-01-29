package server

import (
	"context"
	"fmt"

	"github.com/labring/sealos-state-metrics/pkg/leaderelection"
	log "github.com/sirupsen/logrus"
)

// setupLeaderElection creates and starts the leader elector
func (s *Server) setupLeaderElection() error {
	elector, err := leaderelection.NewLeaderElector(
		s.buildLeaderElectionConfig(),
		s.client,
		log.WithField("component", "leader-election"),
	)
	if err != nil {
		return fmt.Errorf("failed to create leader elector: %w", err)
	}

	elector.SetCallbacks(
		func(ctx context.Context) {
			log.Info("Became leader, starting leader-required collectors")

			if err := s.registry.StartLeaderCollectors(ctx); err != nil {
				log.WithError(err).Error("Failed to start leader-required collectors")
			}
		},
		func() {
			log.Info("Lost leadership, stopping leader-required collectors")

			if err := s.registry.StopLeaderCollectors(); err != nil {
				log.WithError(err).Error("Failed to stop leader-required collectors")
			}
		},
		func(identity string) {
			log.WithField("leader", identity).Info("New leader elected")
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

		log.Info("Starting leader election")

		if err := elector.Run(leCtx); err != nil {
			log.WithError(err).Error("Leader election exited with error")
		}

		log.Info("Leader election stopped")
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
		log.Info("Stopping leader election and releasing lease")
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
