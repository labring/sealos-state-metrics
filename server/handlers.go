package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/labring/sealos-state-metrics/pkg/auth"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

// setupRoutes configures HTTP routes with optional authentication
func (s *Server) setupRoutes(mux *http.ServeMux, metricsPath, healthPath string, enableAuth bool) {
	// Metrics endpoint with optional authentication
	metricsHandler := promhttp.HandlerFor(
		s.promRegistry,
		promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		},
	)

	// Apply authentication middleware if enabled
	if enableAuth {
		authenticator := auth.NewAuthenticator(s.client)
		metricsHandler = authenticator.Middleware(metricsHandler)

		log.Info("Kubernetes authentication enabled for metrics endpoint")
	}

	mux.Handle(metricsPath, metricsHandler)

	// Health endpoint (no authentication)
	mux.HandleFunc(healthPath, s.handleHealth)

	// Collectors list endpoint (no authentication)
	mux.HandleFunc("/collectors", s.handleCollectors)

	// Leader election endpoint (no authentication)
	mux.HandleFunc("/leader", s.handleLeader)

	// Root endpoint (no authentication)
	mux.HandleFunc("/", s.handleRoot)
}

// handleHealth handles health check requests
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	// Get all collectors
	allCollectors := s.registry.GetAllCollectors()

	// Determine which collectors should be checked based on leader election state
	healthStatus := make(map[string]error)
	allHealthy := true

	for name, c := range allCollectors {
		// Determine if this collector should be checked
		var shouldCheck bool

		if !s.config.LeaderElection.Enabled {
			// Leader election disabled: all collectors should be running
			shouldCheck = true
		} else {
			// Leader election enabled
			if c.RequiresLeaderElection() {
				// Leader-required collector: only check if we are the leader
				s.leMu.Lock()
				isLeader := s.leaderElector != nil && s.leaderElector.IsLeader()
				s.leMu.Unlock()

				shouldCheck = isLeader
			} else {
				// Non-leader collector: always check (should always be running)
				shouldCheck = true
			}
		}

		if shouldCheck {
			err := c.Health()

			healthStatus[name] = err
			if err != nil {
				allHealthy = false
			}
		}
	}

	status := http.StatusOK
	if !allHealthy {
		status = http.StatusServiceUnavailable
	}

	writeJSON(w, status, map[string]any{
		"status":     allHealthy,
		"collectors": healthStatus,
	})
}

// handleCollectors handles collector list requests
func (s *Server) handleCollectors(w http.ResponseWriter, _ *http.Request) {
	collectors := s.registry.ListCollectors()
	writeJSON(w, http.StatusOK, map[string]any{
		"collectors": collectors,
		"count":      len(collectors),
	})
}

// handleLeader handles leader election status requests
func (s *Server) handleLeader(w http.ResponseWriter, _ *http.Request) {
	response := map[string]any{
		"enabled": s.config.LeaderElection.Enabled,
	}

	if s.leaderElector != nil {
		response["isLeader"] = s.leaderElector.IsLeader()
		response["currentLeader"] = s.leaderElector.GetLeader()
		response["identity"] = s.leaderElector.GetIdentity()
	} else {
		response["isLeader"] = true
		response["message"] = "Leader election disabled"
	}

	writeJSON(w, http.StatusOK, response)
}

// handleRoot handles root requests
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
	<title>Sealos State Metric</title>
	<style>
		body { font-family: Arial, sans-serif; margin: 40px; }
		h1 { color: #333; }
		a { color: #0066cc; text-decoration: none; margin-right: 20px; }
		a:hover { text-decoration: underline; }
		.info { background: #f0f0f0; padding: 15px; border-radius: 5px; margin-top: 20px; }
	</style>
</head>
<body>
	<h1>Sealos State Metric</h1>
	<p>Production-grade Kubernetes cluster state monitoring system</p>
	<div>
		<a href="%s">Metrics</a>
		<a href="%s">Health</a>
		<a href="/collectors">Collectors</a>
	</div>
</body>
</html>
`, s.config.Server.MetricsPath, s.config.Server.HealthPath)
}

// writeJSON writes a JSON response with the given status code
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.WithError(err).Error("Failed to encode JSON response")
	}
}
