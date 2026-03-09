package middleware_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/labring/sealos-state-metrics/pkg/httpserver/middleware"
	applogger "github.com/labring/sealos-state-metrics/pkg/logger"
	log "github.com/sirupsen/logrus"
)

func TestRequestLoggerInjectsLogger(t *testing.T) {
	gin.SetMode(gin.TestMode)

	buf := &bytes.Buffer{}
	base := log.New()
	base.SetOutput(buf)
	base.SetFormatter(&log.JSONFormatter{})

	router := gin.New()
	router.Use(middleware.RequestLogger(log.NewEntry(base), "test", func() bool { return false }))
	router.GET("/ok", func(c *gin.Context) {
		entry, ok := applogger.EntryFromRequest(c.Request)
		if !ok {
			t.Fatal("expected request logger in context")
		}

		if got := entry.Data["server"]; got != "test" {
			t.Fatalf("unexpected server field: %v", got)
		}

		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ok", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}

	if !bytes.Contains(buf.Bytes(), []byte("[GIN]")) {
		t.Fatalf("expected gin access log, got %q", buf.String())
	}
}

func TestRecoveryRecoversPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)

	buf := &bytes.Buffer{}
	base := log.New()
	base.SetOutput(buf)
	base.SetFormatter(&log.JSONFormatter{})

	entry := log.NewEntry(base)
	router := gin.New()
	router.Use(
		middleware.RequestLogger(entry, "test", func() bool { return false }),
		middleware.Recovery(entry, "test"),
	)
	router.GET("/panic", func(c *gin.Context) {
		panic("boom")
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}

	if rec.Body.Len() != 0 {
		t.Fatalf("expected empty recovery body, got %q", rec.Body.String())
	}

	if !bytes.Contains(buf.Bytes(), []byte("[Recovery] panic recovered")) {
		t.Fatalf("expected recovery log, got %q", buf.String())
	}
}
