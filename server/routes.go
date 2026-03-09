package server

import (
	"fmt"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/labring/sealos-state-metrics/pkg/auth"
	httpmiddleware "github.com/labring/sealos-state-metrics/pkg/httpserver/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

// setupRoutes configures HTTP routes with optional authentication.
func (s *Server) setupRoutes(
	engine *gin.Engine,
	serverName, metricsPath, healthPath string,
	enableAuth bool,
) error {
	metricsHandler := promhttp.HandlerFor(
		s.promRegistry,
		promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		},
	)

	if enableAuth {
		client, err := s.getKubernetesClient()
		if err != nil {
			return fmt.Errorf("failed to get Kubernetes client for authentication: %w", err)
		}

		authenticator := auth.NewAuthenticator(
			client,
			s.logger.WithFields(log.Fields{
				"server":       serverName,
				"subcomponent": "auth",
			}),
		)
		metricsHandler = authenticator.Middleware(metricsHandler)

		s.logger.WithField("server", serverName).
			Info("Kubernetes authentication enabled for metrics endpoint")
	}

	engine.Use(
		httpmiddleware.Recovery(s.logger, serverName),
		httpmiddleware.RequestLogger(
			s.logger,
			serverName,
			cachedLoggerNeedColor(s.logger.Logger),
		),
	)

	engine.Any(metricsPath, gin.WrapH(metricsHandler))
	engine.Any(healthPath, s.handleHealth)
	engine.Any("/collectors", s.handleCollectors)
	engine.Any("/leader", s.handleLeader)
	engine.Any("/", s.handleRoot(metricsPath, healthPath))

	return nil
}

func cachedLoggerNeedColor(l *log.Logger) func() bool {
	needColor := atomic.Bool{}
	cachedFormatter := atomic.Pointer[log.Formatter]{}
	cachedFormatter.Store(&l.Formatter)
	needColor.Store(loggerNeedColor(l.Formatter))

	return func() bool {
		if cachedFormatter.Load() != &l.Formatter {
			cachedFormatter.Store(&l.Formatter)
			color := loggerNeedColor(l.Formatter)
			needColor.Store(color)
			return color
		}

		return needColor.Load()
	}
}

func loggerNeedColor(f log.Formatter) bool {
	if textFormatter, ok := f.(*log.TextFormatter); ok {
		return textFormatter.ForceColors || !textFormatter.DisableColors
	}

	return false
}
