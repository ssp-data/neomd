package main

import (
	"testing"
)

func TestInferIMAPSecurity(t *testing.T) {
	tests := []struct {
		name         string
		port         string
		userSTARTTLS bool
		wantTLS      bool
		wantSTARTTLS bool
		description  string
	}{
		// Standard ports
		{
			name:         "standard IMAPS port 993",
			port:         "993",
			userSTARTTLS: false,
			wantTLS:      true,
			wantSTARTTLS: false,
			description:  "Port 993 should use implicit TLS",
		},
		{
			name:         "standard IMAP port 143",
			port:         "143",
			userSTARTTLS: false,
			wantTLS:      false,
			wantSTARTTLS: true,
			description:  "Port 143 should use STARTTLS",
		},
		// Non-standard ports (Proton Mail Bridge, etc.)
		{
			name:         "Proton Mail Bridge IMAP port 1143",
			port:         "1143",
			userSTARTTLS: false,
			wantTLS:      true,
			wantSTARTTLS: false,
			description:  "Non-standard port 1143 should default to TLS",
		},
		{
			name:         "custom port 1143 with STARTTLS override",
			port:         "1143",
			userSTARTTLS: true,
			wantTLS:      false,
			wantSTARTTLS: true,
			description:  "User override should force STARTTLS even on non-standard port",
		},
		// User config overrides
		{
			name:         "port 993 with STARTTLS override",
			port:         "993",
			userSTARTTLS: true,
			wantTLS:      false,
			wantSTARTTLS: true,
			description:  "User setting starttls=true should override port-based inference",
		},
		{
			name:         "port 143 with STARTTLS override",
			port:         "143",
			userSTARTTLS: true,
			wantTLS:      false,
			wantSTARTTLS: true,
			description:  "Port 143 with starttls=true should use STARTTLS (same as default)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTLS, gotSTARTTLS := inferIMAPSecurity(tt.port, tt.userSTARTTLS)
			if gotTLS != tt.wantTLS {
				t.Errorf("%s: got TLS=%v, want TLS=%v", tt.description, gotTLS, tt.wantTLS)
			}
			if gotSTARTTLS != tt.wantSTARTTLS {
				t.Errorf("%s: got STARTTLS=%v, want STARTTLS=%v", tt.description, gotSTARTTLS, tt.wantSTARTTLS)
			}
		})
	}
}

func TestParseMailtoQuery_PlusPreserved(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		key  string
		want string
	}{
		{
			name: "plus in cc address",
			raw:  "cc=alice%2Bnews@example.com",
			key:  "cc",
			want: "alice+news@example.com",
		},
		{
			name: "literal plus not decoded as space",
			raw:  "subject=a+b+c",
			key:  "subject",
			want: "a+b+c",
		},
		{
			name: "percent-encoded space",
			raw:  "subject=hello%20world",
			key:  "subject",
			want: "hello world",
		},
		{
			name: "multiple params with plus",
			raw:  "cc=a%2Bb@x.com&subject=re%3A+test",
			key:  "cc",
			want: "a+b@x.com",
		},
		{
			name: "empty query",
			raw:  "",
			key:  "cc",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := parseMailtoQuery(tt.raw)
			got := q(tt.key)
			if got != tt.want {
				t.Errorf("parseMailtoQuery(%q)(%q) = %q, want %q", tt.raw, tt.key, got, tt.want)
			}
		})
	}
}

func TestParseMailto_PlusInAddress(t *testing.T) {
	params := parseMailto("mailto:user@example.com?cc=alice%2Bnews@example.com&subject=a+b")
	if params.CC != "alice+news@example.com" {
		t.Errorf("CC = %q, want %q", params.CC, "alice+news@example.com")
	}
	// '+' in subject should stay literal, not become space
	if params.Subject != "a+b" {
		t.Errorf("Subject = %q, want %q", params.Subject, "a+b")
	}
}
