package util

import (
	"context"
	"errors"
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

func TestRetryDialContext_RetriesUntilSuccess(t *testing.T) {
	attempts := 0
	wantConn := &mockConn{}
	dialContext := retryDialContext(
		func(context.Context, string, string) (net.Conn, error) {
			attempts++
			if attempts < 3 {
				return nil, errors.New("temporary dial failure")
			}

			return wantConn, nil
		},
		3,
	)

	conn, err := dialContext(context.Background(), "tcp", "127.0.0.1:443")
	if err != nil {
		t.Fatalf("retry dial returned error: %v", err)
	}

	if conn != wantConn {
		t.Fatalf("retry dial returned %#v, want %#v", conn, wantConn)
	}

	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestRetryDialContext_StopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0

	dialContext := retryDialContext(
		func(context.Context, string, string) (net.Conn, error) {
			attempts++
			cancel()
			return nil, errors.New("temporary dial failure")
		},
		3,
	)

	if _, err := dialContext(ctx, "tcp", "127.0.0.1:443"); err == nil {
		t.Fatal("retry dial returned nil error, want error")
	}

	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestRetryDialContext_NormalizesInvalidRetries(t *testing.T) {
	attempts := 0
	dialContext := retryDialContext(
		func(context.Context, string, string) (net.Conn, error) {
			attempts++
			return nil, errors.New("dial failed")
		},
		0,
	)

	if _, err := dialContext(context.Background(), "tcp", "127.0.0.1:443"); err == nil {
		t.Fatal("retry dial returned nil error, want error")
	}

	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
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

type mockConn struct{}

func (*mockConn) Read([]byte) (int, error) {
	return 0, errors.New("not implemented")
}

func (*mockConn) Write([]byte) (int, error) {
	return 0, errors.New("not implemented")
}

func (*mockConn) Close() error {
	return nil
}

func (*mockConn) LocalAddr() net.Addr {
	return mockAddr("local")
}

func (*mockConn) RemoteAddr() net.Addr {
	return mockAddr("remote")
}

func (*mockConn) SetDeadline(time.Time) error {
	return nil
}

func (*mockConn) SetReadDeadline(time.Time) error {
	return nil
}

func (*mockConn) SetWriteDeadline(time.Time) error {
	return nil
}

type mockAddr string

func (a mockAddr) Network() string {
	return string(a)
}

func (a mockAddr) String() string {
	return string(a)
}
