package server

import (
	"fmt"
)

// startCollectors starts collectors with or without leader election
func (s *Server) startCollectors() error {
	if !s.config.LeaderElection.Enabled {
		s.logger.Info("Leader election disabled, starting all collectors")
		return s.registry.Start(s.serverCtx)
	}

	// Start non-leader collectors immediately
	if err := s.registry.StartNonLeaderCollectors(s.serverCtx); err != nil {
		s.logger.WithError(err).Warn("Some non-leader collectors failed to start")
	}

	// Setup leader election
	return s.setupLeaderElection()
}

// stopCollectors stops all collectors based on current leader election configuration
func (s *Server) stopCollectors() error {
	logger := s.logger

	if s.config.LeaderElection.Enabled {
		// Current state: leader election is enabled
		// Stop leader election first (will trigger OnStoppedLeading callback to stop leader collectors)
		s.stopLeaderElection()
		// Then stop non-leader collectors
		if err := s.registry.StopNonLeaderCollectors(); err != nil {
			logger.WithError(err).Warn("Failed to stop non-leader collectors")
			return err
		}
	} else {
		// Current state: leader election is disabled
		// All collectors were started without leader election, stop them all
		if err := s.registry.Stop(); err != nil {
			logger.WithError(err).Warn("Failed to stop collectors")
			return err
		}
	}

	return nil
}

// reinitializeAndStartCollectors reinitializes collectors and sets up leader election.
// IMPORTANT: Caller (Reload) must hold s.mu lock.
func (s *Server) reinitializeAndStartCollectors() error {
	// Reinitialize collectors (creates new collector instances)
	if err := s.registry.Reinitialize(s.buildInitConfig()); err != nil {
		return fmt.Errorf("failed to reinitialize collectors: %w", err)
	}

	// Start collectors with new configuration
	if err := s.startCollectors(); err != nil {
		return fmt.Errorf("failed to start collectors: %w", err)
	}

	return nil
}
