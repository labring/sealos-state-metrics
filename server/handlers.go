package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// handleHealth handles health check requests.
func (s *Server) handleHealth(c *gin.Context) {
	allCollectors := s.registry.GetAllCollectors()
	failedCollectors := s.registry.GetFailedCollectors()
	healthStatus := make(map[string]string)
	allHealthy := true

	s.leMu.Lock()
	isLeader := s.leaderElector != nil && s.leaderElector.IsLeader()
	s.leMu.Unlock()

	for name, collector := range allCollectors {
		var shouldCheck bool
		switch {
		case !s.config.LeaderElection.Enabled:
			shouldCheck = true
		case collector.RequiresLeaderElection():
			shouldCheck = isLeader
		default:
			shouldCheck = true
		}

		if shouldCheck {
			if err := collector.Health(); err != nil {
				healthStatus[name] = err.Error()
				allHealthy = false
			}
		}
	}

	for name, err := range failedCollectors {
		healthStatus[name] = err.Error()
		allHealthy = false
	}

	status := http.StatusOK
	if !allHealthy {
		status = http.StatusServiceUnavailable
	}

	c.JSON(status, gin.H{
		"status":    allHealthy,
		"unhealthy": healthStatus,
	})
}

// handleCollectors handles collector list requests.
func (s *Server) handleCollectors(c *gin.Context) {
	collectors := s.registry.ListCollectors()
	c.JSON(http.StatusOK, gin.H{
		"collectors": collectors,
		"count":      len(collectors),
	})
}

// handleLeader handles leader election status requests.
func (s *Server) handleLeader(c *gin.Context) {
	response := gin.H{
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

	c.JSON(http.StatusOK, response)
}

// handleRoot handles root requests.
func (s *Server) handleRoot(metricsPath, healthPath string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path != "/" {
			c.Status(http.StatusNotFound)
			return
		}

		c.Header("Content-Type", "text/html")
		c.String(http.StatusOK, `
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
`, metricsPath, healthPath)
	}
}
