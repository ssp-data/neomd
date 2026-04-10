package mailtls

import (
	"crypto/x509"
	"errors"
	"testing"
)

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{host: "localhost", want: true},
		{host: "LOCALHOST", want: true},
		{host: "127.0.0.1", want: true},
		{host: "::1", want: true},
		{host: "192.168.1.10", want: false},
		{host: "imap.gmail.com", want: false},
	}

	for _, tt := range tests {
		if got := IsLoopbackHost(tt.host); got != tt.want {
			t.Fatalf("IsLoopbackHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestShouldRetryInsecureLocalhost(t *testing.T) {
	if !ShouldRetryInsecureLocalhost("127.0.0.1", "", x509.UnknownAuthorityError{}) {
		t.Fatal("expected localhost unknown authority error to trigger fallback")
	}
	if ShouldRetryInsecureLocalhost("imap.example.com", "", x509.UnknownAuthorityError{}) {
		t.Fatal("did not expect remote host to trigger fallback")
	}
	if ShouldRetryInsecureLocalhost("127.0.0.1", "/tmp/cert.pem", x509.UnknownAuthorityError{}) {
		t.Fatal("did not expect explicit tls_cert_file to trigger fallback")
	}
	if ShouldRetryInsecureLocalhost("127.0.0.1", "", errors.New("timeout")) {
		t.Fatal("did not expect non-certificate error to trigger fallback")
	}
}
