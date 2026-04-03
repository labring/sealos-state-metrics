package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/labring/sealos-state-metrics/pkg/leaderelection"
	log "github.com/sirupsen/logrus"
)

type collectorLeaderElector struct {
	collectorName string
	leaseName     string
	elector       *leaderelection.LeaderElector
	cancel        context.CancelFunc
	doneCh        chan struct{}
}

// setupLeaderElections creates and starts one leader elector per leader-managed collector.
func (s *Server) setupLeaderElections() error {
	// Get Kubernetes client via shared client provider
	client, err := s.getKubernetesClient()
	if err != nil {
		return fmt.Errorf("failed to get Kubernetes client for leader election: %w", err)
	}

	collectorNames := s.registry.ListCollectorsByLeaderRequirement(true)
	if len(collectorNames) == 0 {
		s.logger.Info("Leader election enabled but no leader-managed collectors are registered")
		return nil
	}

	handles := make(map[string]*collectorLeaderElector, len(collectorNames))

	for _, collectorName := range collectorNames {

		cfg := s.buildLeaderElectionConfig(collectorName)
		leaseName := cfg.LeaseName

		elector, err := leaderelection.NewLeaderElector(
			cfg,
			client,
			s.logger.WithFields(log.Fields{
				"subcomponent": "leader-election",
				"collector":    collectorName,
				"leaseName":    leaseName,
			}),
		)
		if err != nil {
			for _, handle := range handles {
				handle.cancel()
			}

			for _, handle := range handles {
				<-handle.doneCh
			}

			return fmt.Errorf(
				"failed to create leader elector for collector %s: %w",
				collectorName,
				err,
			)
		}

		elector.SetCallbacks(
			func(ctx context.Context) {
				s.logger.WithFields(log.Fields{
					"collector": collectorName,
					"leaseName": leaseName,
				}).Info("Became leader for collector, starting collector")

				if err := s.registry.StartCollector(ctx, collectorName); err != nil {
					s.logger.WithError(err).
						WithField("collector", collectorName).
						Error("Failed to start collector after leadership acquired")
				}
			},
			func() {
				s.logger.WithFields(log.Fields{
					"collector": collectorName,
					"leaseName": leaseName,
				}).Info("Lost leadership for collector, stopping collector")

				if err := s.registry.StopCollector(collectorName); err != nil {
					s.logger.WithError(err).
						WithField("collector", collectorName).
						Error("Failed to stop collector after leadership lost")
				}
			},
			func(identity string) {
				s.logger.WithFields(log.Fields{
					"collector": collectorName,
					"leaseName": leaseName,
					"leader":    identity,
				}).Info("New leader elected for collector")
			},
		)

		leCtx, cancel := context.WithCancel(s.serverCtx)
		doneCh := make(chan struct{})
		handle := &collectorLeaderElector{
			collectorName: collectorName,
			leaseName:     leaseName,
			elector:       elector,
			cancel:        cancel,
			doneCh:        doneCh,
		}

		handles[collectorName] = handle

		go func(h *collectorLeaderElector, ctx context.Context) {
			defer close(h.doneCh)

			s.logger.WithFields(log.Fields{
				"collector": h.collectorName,
				"leaseName": h.leaseName,
			}).Info("Starting leader election for collector")

			if err := h.elector.Run(ctx); err != nil {
				s.logger.WithError(err).
					WithField("collector", h.collectorName).
					Error("Leader election exited with error")
			}

			s.logger.WithFields(log.Fields{
				"collector": h.collectorName,
				"leaseName": h.leaseName,
			}).Info("Leader election for collector stopped")
		}(handle, leCtx)
	}

	s.leMu.Lock()
	s.leaderElectors = handles
	s.leMu.Unlock()

	return nil
}

// stopLeaderElections stops all current leader elections and releases their leases.
func (s *Server) stopLeaderElections() {
	s.leMu.Lock()
	handles := s.leaderElectors
	s.leaderElectors = make(map[string]*collectorLeaderElector)
	s.leMu.Unlock()

	if len(handles) == 0 {
		return
	}

	s.logger.WithField("count", len(handles)).Info("Stopping leader elections and releasing leases")

	for _, handle := range handles {
		handle.cancel()
	}

	for _, handle := range handles {
		<-handle.doneCh
	}
}

func (s *Server) isCollectorLeader(collectorName string) bool {
	s.leMu.RLock()
	defer s.leMu.RUnlock()

	handle, exists := s.leaderElectors[collectorName]
	if !exists || handle.elector == nil {
		return false
	}

	return handle.elector.IsLeader()
}

func (s *Server) leaderStatuses() map[string]map[string]any {
	s.leMu.RLock()
	defer s.leMu.RUnlock()

	statuses := make(map[string]map[string]any, len(s.leaderElectors))
	for name, handle := range s.leaderElectors {
		statuses[name] = map[string]any{
			"isLeader":      handle.elector.IsLeader(),
			"currentLeader": handle.elector.GetLeader(),
			"identity":      handle.elector.GetIdentity(),
			"leaseName":     handle.leaseName,
		}
	}

	return statuses
}

func collectorLeaseName(baseLeaseName, collectorName string) string {
	const maxNameLength = 63

	raw := fmt.Sprintf("%s-%s", baseLeaseName, collectorName)
	if len(raw) <= maxNameLength {
		return raw
	}

	sum := sha1.Sum([]byte(raw))
	hashSuffix := hex.EncodeToString(sum[:4])

	collectorPart := sanitizeLeasePart(collectorName)
	if len(collectorPart) > 16 {
		collectorPart = collectorPart[:16]
	}

	suffix := fmt.Sprintf("%s-%s", collectorPart, hashSuffix)

	prefixBudget := maxNameLength - len(suffix) - 1
	if prefixBudget <= 0 {
		if len(suffix) > maxNameLength {
			return suffix[:maxNameLength]
		}
		return suffix
	}

	prefix := sanitizeLeasePart(baseLeaseName)
	if len(prefix) > prefixBudget {
		prefix = prefix[:prefixBudget]
	}

	prefix = strings.Trim(prefix, "-")
	if prefix == "" {
		return suffix
	}

	return fmt.Sprintf("%s-%s", prefix, suffix)
}

func sanitizeLeasePart(value string) string {
	value = strings.ToLower(value)

	value = strings.Trim(value, "-")
	if value == "" {
		return "collector"
	}

	return value
}
