package main

import "testing"

func TestInferIMAPSecurity(t *testing.T) {
	tests := []struct {
		name          string
		port          string
		userSTARTTLS  bool
		wantTLS       bool
		wantSTARTTLS  bool
		description   string
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
