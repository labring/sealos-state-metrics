package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	applogger "github.com/labring/sealos-state-metrics/pkg/logger"
	log "github.com/sirupsen/logrus"
)

const requestAtKey = "requestAt"

// SetRequestAt stores the request start time in gin context.
func SetRequestAt(c *gin.Context, requestAt time.Time) {
	c.Set(requestAtKey, requestAt)
}

// GetRequestAt returns the request start time from gin context.
func GetRequestAt(c *gin.Context) time.Time {
	return c.GetTime(requestAtKey)
}

// RequestLogger logs access lines in the same format as aiproxy's gin logger while
// keeping the request-scoped logger injected through request context.
func RequestLogger(base *log.Entry, serverName string, needColor func() bool) gin.HandlerFunc {
	if base == nil {
		base = log.WithField("component", "http")
	}

	return func(c *gin.Context) {
		start := time.Now()
		SetRequestAt(c, start)

		requestEntry := base.WithFields(log.Fields{
			"server": serverName,
		})

		c.Request = applogger.WithRequestEntry(c.Request, requestEntry)
		c.Next()

		path := c.Request.URL.Path
		if raw := c.Request.URL.RawQuery; raw != "" {
			path += "?" + raw
		}

		params := gin.LogFormatterParams{
			Request: c.Request,
			Keys:    c.Keys,
		}
		params.Latency = time.Since(start)
		params.ClientIP = c.ClientIP()
		params.Method = c.Request.Method
		params.StatusCode = c.Writer.Status()
		params.ErrorMessage = c.Errors.ByType(gin.ErrorTypePrivate).String()
		params.BodySize = c.Writer.Size()
		params.Path = path

		logAccess(requestEntry, params, needColor)
	}
}

func logAccess(entry *log.Entry, params gin.LogFormatterParams, needColor func() bool) {
	line := formatAccessLog(params, needColor)

	switch code := params.StatusCode; {
	case code >= http.StatusInternalServerError:
		entry.Error(line)
	case code >= http.StatusBadRequest:
		entry.Warn(line)
	default:
		entry.Info(line)
	}
}

func formatAccessLog(params gin.LogFormatterParams, needColor func() bool) string {
	var statusColor, methodColor, resetColor string
	if needColor() {
		statusColor = params.StatusCodeColor()
		methodColor = params.MethodColor()
		resetColor = params.ResetColor()
	}

	return fmt.Sprintf("[GIN] |%s %3d %s| %10v | %15s |%s %-7s %s %#v\n%s",
		statusColor, params.StatusCode, resetColor,
		truncateDuration(params.Latency),
		params.ClientIP,
		methodColor, params.Method, resetColor,
		params.Path,
		params.ErrorMessage,
	)
}

func truncateDuration(d time.Duration) time.Duration {
	if d > time.Hour {
		return d.Truncate(time.Minute)
	}

	if d > time.Minute {
		return d.Truncate(time.Second)
	}

	if d > time.Second {
		return d.Truncate(time.Millisecond)
	}

	if d > time.Millisecond {
		return d.Truncate(time.Microsecond)
	}

	return d
}
