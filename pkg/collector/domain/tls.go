package domain

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/labring/sealos-state-metrics/pkg/util"
)

func getTLSCertWithIPAndRetries(
	host string,
	port int,
	ip string,
	skipTLSVerify bool,
	timeout time.Duration,
	dialRetries int,
) (*util.CertInfo, error) {
	if err := validateHTTPMonitoringTarget(host, port, ip); err != nil {
		return nil, fmt.Errorf("invalid request target: %w", err)
	}

	dialer := &tls.Dialer{
		Config: &tls.Config{
			//nolint:gosec // Domain collector intentionally supports per-target TLS verification bypass.
			InsecureSkipVerify: skipTLSVerify,
			MinVersion:         tls.VersionTLS12,
			ServerName:         host,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	dialContext := retryHTTPDialContext(dialer.DialContext, dialRetries)

	conn, err := dialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}
	defer conn.Close()

	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return nil, errors.New("not a TLS connection")
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, errors.New("no certificates found")
	}

	cert := state.PeerCertificates[0]
	now := time.Now()
	expiresIn := cert.NotAfter.Sub(now)
	isValid := now.After(cert.NotBefore) && now.Before(cert.NotAfter)

	return &util.CertInfo{
		CommonName: cert.Subject.CommonName,
		Issuer:     cert.Issuer.CommonName,
		NotBefore:  cert.NotBefore,
		NotAfter:   cert.NotAfter,
		ExpiresIn:  expiresIn,
		IsValid:    isValid,
	}, nil
}
