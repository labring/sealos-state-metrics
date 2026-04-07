package util

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestCheckHTTPWithIP_FollowRedirectsFallsBackToDefaultDialForDifferentHost(t *testing.T) {
	redirectTarget := newLocalTLSServerOnAddr(
		t,
		"127.0.0.1:0",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	defer redirectTarget.Close()

	redirectURL, err := url.Parse(redirectTarget.URL)
	if err != nil {
		t.Fatalf("failed to parse redirect target URL: %v", err)
	}

	redirectURL.Host = net.JoinHostPort("localhost", redirectURL.Port())

	origin := newLocalTLSServerOnAddr(
		t,
		"127.0.0.1:0",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, redirectURL.String(), http.StatusFound)
		}),
	)
	defer origin.Close()

	originURL, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatalf("failed to parse origin URL: %v", err)
	}

	result := CheckHTTPWithIP(
		context.Background(),
		originURL.Hostname(),
		mustURLPort(t, originURL),
		originURL.Hostname(),
		true,
		5*time.Second,
		true,
	)

	if !result.Success {
		t.Fatalf("CheckHTTPWithIP() failed: %#v", result)
	}

	if result.StatusCode != http.StatusNoContent {
		t.Fatalf("result.StatusCode = %d, want %d", result.StatusCode, http.StatusNoContent)
	}

	if redirectURL.Hostname() == originURL.Hostname() {
		t.Fatal("redirect target hostname unexpectedly matches origin hostname")
	}
}

func TestCheckHTTPWithIP_CanDisableRedirectFollowing(t *testing.T) {
	origin := newLocalTLSServerOnAddr(
		t,
		"127.0.0.1:0",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://localhost/next", http.StatusFound)
		}),
	)
	defer origin.Close()

	originURL, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatalf("failed to parse origin URL: %v", err)
	}

	result := CheckHTTPWithIP(
		context.Background(),
		originURL.Hostname(),
		mustURLPort(t, originURL),
		originURL.Hostname(),
		true,
		5*time.Second,
		false,
	)

	if !result.Success {
		t.Fatalf("CheckHTTPWithIP() failed: %#v", result)
	}

	if result.StatusCode != http.StatusFound {
		t.Fatalf("result.StatusCode = %d, want %d", result.StatusCode, http.StatusFound)
	}
}

func newLocalTLSServerOnAddr(t *testing.T, addr string, handler http.Handler) *httptest.Server {
	t.Helper()

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen on %q: %v", addr, err)
	}

	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.StartTLS()

	return server
}

func mustURLPort(t *testing.T, parsedURL *url.URL) int {
	t.Helper()

	port, err := strconv.Atoi(parsedURL.Port())
	if err != nil {
		t.Fatalf("failed to parse URL port %q: %v", parsedURL.Port(), err)
	}

	return port
}
