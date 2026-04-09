package smtp

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sspaeti/neomd/internal/render"
)

// parseMIME parses raw message bytes into a mail.Message and its top-level
// media type and params. Fails the test on any error.
func parseMIME(t *testing.T, raw []byte) (*mail.Message, string, map[string]string) {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("ParseMediaType: %v", err)
	}
	return msg, mediaType, params
}

// readParts reads all parts from a multipart reader and returns them.
func readParts(t *testing.T, r *multipart.Reader) []*multipart.Part {
	t.Helper()
	var parts []*multipart.Part
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		parts = append(parts, p)
	}
	return parts
}

// create1x1PNG writes a minimal 1x1 red PNG to path.
func create1x1PNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create png: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
}

func TestBuildMessage_PlainOnly(t *testing.T) {
	raw, err := buildMessage(
		"Alice <alice@example.com>",
		"Bob <bob@example.com>",
		"",
		"Hello",
		"plain body",
		"<p>html body</p>",
		nil,
	)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}

	_, mediaType, params := parseMIME(t, raw)
	if mediaType != "multipart/alternative" {
		t.Fatalf("expected multipart/alternative, got %s", mediaType)
	}

	msg, _ := mail.ReadMessage(bytes.NewReader(raw))
	mr := multipart.NewReader(msg.Body, params["boundary"])
	parts := readParts(t, mr)

	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}

	ct0, _, _ := mime.ParseMediaType(parts[0].Header.Get("Content-Type"))
	ct1, _, _ := mime.ParseMediaType(parts[1].Header.Get("Content-Type"))

	if ct0 != "text/plain" {
		t.Errorf("part 0: expected text/plain, got %s", ct0)
	}
	if ct1 != "text/html" {
		t.Errorf("part 1: expected text/html, got %s", ct1)
	}
}

func TestBuildMessage_WithAttachment(t *testing.T) {
	dir := t.TempDir()
	attPath := filepath.Join(dir, "readme.txt")
	if err := os.WriteFile(attPath, []byte("file content"), 0644); err != nil {
		t.Fatal(err)
	}

	raw, err := buildMessage(
		"Alice <alice@example.com>",
		"Bob <bob@example.com>",
		"",
		"With attachment",
		"plain body",
		"<p>html body</p>",
		[]string{attPath},
	)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}

	_, mediaType, params := parseMIME(t, raw)
	if mediaType != "multipart/mixed" {
		t.Fatalf("expected multipart/mixed, got %s", mediaType)
	}

	msg, _ := mail.ReadMessage(bytes.NewReader(raw))
	mr := multipart.NewReader(msg.Body, params["boundary"])

	// Part 0: multipart/alternative
	part0, err := mr.NextPart()
	if err != nil {
		t.Fatalf("NextPart 0: %v", err)
	}
	ct0, p0, _ := mime.ParseMediaType(part0.Header.Get("Content-Type"))
	if ct0 != "multipart/alternative" {
		t.Fatalf("first part: expected multipart/alternative, got %s", ct0)
	}
	// Read the nested alternative parts before advancing the outer reader.
	altMR := multipart.NewReader(part0, p0["boundary"])
	altParts := readParts(t, altMR)
	if len(altParts) != 2 {
		t.Fatalf("expected 2 alternative parts, got %d", len(altParts))
	}

	// Part 1: the file attachment
	part1, err := mr.NextPart()
	if err != nil {
		t.Fatalf("NextPart 1: %v", err)
	}
	ct1, _, _ := mime.ParseMediaType(part1.Header.Get("Content-Type"))
	if ct1 != "text/plain" {
		t.Errorf("attachment content-type: expected text/plain, got %s", ct1)
	}
	disp := part1.Header.Get("Content-Disposition")
	if !strings.Contains(disp, "readme.txt") {
		t.Errorf("attachment disposition missing filename: %s", disp)
	}

	// Verify attachment content round-trips through base64
	attData, _ := io.ReadAll(part1)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(attData)))
	if err != nil {
		t.Fatalf("decode attachment: %v", err)
	}
	if string(decoded) != "file content" {
		t.Errorf("attachment content: got %q, want %q", decoded, "file content")
	}

	// Verify no more parts
	if _, err := mr.NextPart(); err != io.EOF {
		t.Error("expected exactly 2 top-level parts")
	}
}

func TestBuildMessage_WithInlineImage(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "pixel.png")
	create1x1PNG(t, imgPath)

	// Use goldmark to convert markdown containing the image reference.
	markdown := fmt.Sprintf("![img](%s)", imgPath)
	htmlBody, err := render.ToHTML(markdown)
	if err != nil {
		t.Fatalf("ToHTML: %v", err)
	}

	// Verify goldmark produced an img tag with the local path.
	if !strings.Contains(htmlBody, fmt.Sprintf(`src="%s"`, imgPath)) {
		t.Fatalf("expected local img src in HTML, got:\n%s", htmlBody)
	}

	raw, err := buildMessage(
		"Alice <alice@example.com>",
		"Bob <bob@example.com>",
		"",
		"Inline image",
		markdown,
		htmlBody,
		nil,
	)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}

	_, mediaType, params := parseMIME(t, raw)
	if mediaType != "multipart/related" {
		t.Fatalf("expected multipart/related, got %s", mediaType)
	}

	msg, _ := mail.ReadMessage(bytes.NewReader(raw))
	mr := multipart.NewReader(msg.Body, params["boundary"])
	parts := readParts(t, mr)

	if len(parts) != 2 {
		t.Fatalf("expected 2 parts (alternative + inline image), got %d", len(parts))
	}

	// First part: multipart/alternative
	ct0, _, _ := mime.ParseMediaType(parts[0].Header.Get("Content-Type"))
	if ct0 != "multipart/alternative" {
		t.Errorf("first part: expected multipart/alternative, got %s", ct0)
	}

	// Second part: inline image with Content-ID
	ct1, _, _ := mime.ParseMediaType(parts[1].Header.Get("Content-Type"))
	if ct1 != "image/png" {
		t.Errorf("image part: expected image/png, got %s", ct1)
	}
	cid := parts[1].Header.Get("Content-Id")
	if cid == "" {
		t.Error("image part missing Content-ID header")
	}
	if !strings.Contains(cid, "img0@neomd") {
		t.Errorf("unexpected Content-ID: %s", cid)
	}

	// Verify the HTML was rewritten from local path to cid:
	if strings.Contains(string(raw), fmt.Sprintf(`src="%s"`, imgPath)) {
		t.Error("HTML still contains local path instead of cid: reference")
	}
	if !strings.Contains(string(raw), "cid:img0@neomd") {
		t.Error("HTML does not contain expected cid:img0@neomd reference")
	}
}

func TestBuildMessage_Headers(t *testing.T) {
	raw, err := buildMessage(
		"Alice <alice@example.com>",
		"Bob <bob@example.com>",
		"",
		"Test Subject",
		"body",
		"<p>body</p>",
		nil,
	)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}

	msg, _, _ := parseMIME(t, raw)

	checks := map[string]string{
		"From":         "Alice <alice@example.com>",
		"To":           "Bob <bob@example.com>",
		"MIME-Version": "1.0",
		"X-Mailer":     "neomd",
	}
	for hdr, want := range checks {
		got := msg.Header.Get(hdr)
		if got != want {
			t.Errorf("header %s: got %q, want %q", hdr, got, want)
		}
	}

	// Subject is Q-encoded, verify it decodes correctly
	subj := msg.Header.Get("Subject")
	if subj == "" {
		t.Error("Subject header missing")
	}

	// Date must be present and non-empty
	if msg.Header.Get("Date") == "" {
		t.Error("Date header missing")
	}

	// Message-ID must be present
	if msg.Header.Get("Message-Id") == "" {
		t.Error("Message-ID header missing")
	}
}

func TestBuildMessage_CCHeader(t *testing.T) {
	raw, err := buildMessage(
		"Alice <alice@example.com>",
		"Bob <bob@example.com>",
		"Carol <carol@example.com>",
		"CC test",
		"body",
		"<p>body</p>",
		nil,
	)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}

	msg, _, _ := parseMIME(t, raw)
	cc := msg.Header.Get("Cc")
	if cc != "Carol <carol@example.com>" {
		t.Errorf("Cc header: got %q, want %q", cc, "Carol <carol@example.com>")
	}
}

func TestBuildMessage_NoBccInHeaders(t *testing.T) {
	// buildMessage does not accept a bcc parameter, so Bcc should never appear.
	raw, err := buildMessage(
		"Alice <alice@example.com>",
		"Bob <bob@example.com>",
		"",
		"No BCC",
		"body",
		"<p>body</p>",
		nil,
	)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}

	msg, _, _ := parseMIME(t, raw)
	if bcc := msg.Header.Get("Bcc"); bcc != "" {
		t.Errorf("Bcc header should be absent, got %q", bcc)
	}

	// Also scan raw bytes for any Bcc header line
	if strings.Contains(strings.ToLower(string(raw)), "\nbcc:") ||
		strings.HasPrefix(strings.ToLower(string(raw)), "bcc:") {
		t.Error("raw message contains Bcc header line")
	}
}

func TestExtractAddr(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "name and angle brackets",
			input: "Alice <alice@example.com>",
			want:  "alice@example.com",
		},
		{
			name:  "bare address",
			input: "alice@example.com",
			want:  "alice@example.com",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "with leading space",
			input: "  Bob <bob@test.org>",
			want:  "bob@test.org",
		},
		{
			name:  "angle brackets no name",
			input: "<solo@domain.com>",
			want:  "solo@domain.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractAddr(tt.input)
			if got != tt.want {
				t.Errorf("extractAddr(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestInferSMTPUseTLS(t *testing.T) {
	tests := []struct {
		name         string
		port         string
		userSTARTTLS bool
		wantTLS      bool
		description  string
	}{
		// Standard ports
		{
			name:         "standard SMTPS port 465",
			port:         "465",
			userSTARTTLS: false,
			wantTLS:      true,
			description:  "Port 465 should use implicit TLS",
		},
		{
			name:         "standard submission port 587",
			port:         "587",
			userSTARTTLS: false,
			wantTLS:      false,
			description:  "Port 587 should use STARTTLS",
		},
		// Non-standard ports (Proton Mail Bridge, etc.)
		{
			name:         "Proton Mail Bridge SMTP port 1025",
			port:         "1025",
			userSTARTTLS: false,
			wantTLS:      true,
			description:  "Non-standard port 1025 should default to TLS (user must set starttls=true if needed)",
		},
		{
			name:         "custom port 1025 with STARTTLS override",
			port:         "1025",
			userSTARTTLS: true,
			wantTLS:      false,
			description:  "User setting starttls=true should force STARTTLS on non-standard port",
		},
		// User config overrides
		{
			name:         "port 465 with STARTTLS override",
			port:         "465",
			userSTARTTLS: true,
			wantTLS:      false,
			description:  "User setting starttls=true should override port 465 default",
		},
		{
			name:         "port 587 with STARTTLS override",
			port:         "587",
			userSTARTTLS: true,
			wantTLS:      false,
			description:  "Port 587 with starttls=true should use STARTTLS (same as default)",
		},
		{
			name:         "port 587 with starttls false",
			port:         "587",
			userSTARTTLS: false,
			wantTLS:      false,
			description:  "Port 587 should use STARTTLS even when starttls=false (port takes precedence)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferSMTPUseTLS(tt.port, tt.userSTARTTLS)
			if got != tt.wantTLS {
				t.Errorf("%s: got TLS=%v, want TLS=%v", tt.description, got, tt.wantTLS)
			}
		})
	}
}
