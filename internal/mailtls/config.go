package mailtls

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
)

// Config returns a strict TLS config for the given host.
// If certFile is set, it is added to the system root pool.
func Config(host, certFile string) (*tls.Config, error) {
	cfg := &tls.Config{ServerName: host}
	if certFile == "" {
		return cfg, nil
	}

	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system cert pool: %w", err)
	}
	if pool == nil {
		pool = x509.NewCertPool()
	}

	pem, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("read tls_cert_file %q: %w", certFile, err)
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("parse tls_cert_file %q: no PEM certificates found", certFile)
	}
	cfg.RootCAs = pool
	return cfg, nil
}

// InsecureLocalhostConfig returns a loopback-only fallback config for local bridges
// that use a self-signed certificate, such as Proton Mail Bridge.
func InsecureLocalhostConfig(host string) *tls.Config {
	return &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true,
	}
}

// ShouldRetryInsecureLocalhost reports whether a strict TLS failure should be
// retried once with loopback-only insecure verification.
func ShouldRetryInsecureLocalhost(host, certFile string, err error) bool {
	if certFile != "" || !IsLoopbackHost(host) || err == nil {
		return false
	}

	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "x509") && strings.Contains(msg, "certificate signed by")
}

func IsLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
