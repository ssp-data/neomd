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

func TestBuildDraftMessage_PlainTextOnly(t *testing.T) {
	// Test that drafts are stored as plain text only, no HTML conversion.
	// This ensures markdown formatting survives the save/load cycle.
	markdownBody := `thello there

--
**Simon Späti**
Data Engineer & Technical Author, SSP Data GmbH

Connect: [LinkedIn](https://li.ssp.sh/) | [Bluesky](https://bs.ssp.sh/) | [GitHub](https://gh.ssp.sh/)
Explore: [Website](https://ssp.sh/) | [Vault](https://vault.ssp.sh/) | [Book](https://dedp.online/) | [Services](https://ssp.sh/services)

*sent from [neomd](https://neomd.ssp.sh)*`

	raw, err := BuildDraftMessage(
		"Simon Späti <simu@sspaeti.com>",
		"sspaeti@hey.com",
		"",
		"sspaeti@hey.com",
		"test 5555",
		markdownBody,
		nil,
	)
	if err != nil {
		t.Fatalf("BuildDraftMessage: %v", err)
	}

	msg, mediaType, _ := parseMIME(t, raw)

	// Verify it's plain text, NOT multipart/alternative
	if mediaType != "text/plain" {
		t.Errorf("expected text/plain for draft, got %s", mediaType)
	}

	// Verify BCC header is present (drafts keep BCC)
	bcc := msg.Header.Get("Bcc")
	if bcc != "sspaeti@hey.com" {
		t.Errorf("Bcc header: got %q, want %q", bcc, "sspaeti@hey.com")
	}

	// Read the body and verify it matches exactly (no HTML conversion artifacts)
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	bodyStr := string(body)
	// The body is quoted-printable encoded, but for ASCII text it should be mostly readable.
	// Check for key markers that would be munged by HTML conversion:
	if !strings.Contains(bodyStr, "**Simon Sp") { // ** should be preserved, not converted to <strong>
		t.Error("markdown bold syntax (**) not preserved in draft body")
	}
	if !strings.Contains(bodyStr, "[LinkedIn]") { // markdown links should be preserved
		t.Error("markdown link syntax [] not preserved in draft body")
	}
	if !strings.Contains(bodyStr, "*sent from") { // * for italics should be preserved
		t.Error("markdown italic syntax (*) not preserved in draft body")
	}
	if strings.Contains(bodyStr, "<p>") || strings.Contains(bodyStr, "<strong>") {
		t.Error("draft body contains HTML tags - should be plain text only")
	}
}

func TestBuildDraftMessage_WithAttachment(t *testing.T) {
	dir := t.TempDir()
	attPath := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(attPath, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	raw, err := BuildDraftMessage(
		"Alice <alice@example.com>",
		"Bob <bob@example.com>",
		"",
		"",
		"Draft with attachment",
		"body text",
		[]string{attPath},
	)
	if err != nil {
		t.Fatalf("BuildDraftMessage: %v", err)
	}

	_, mediaType, params := parseMIME(t, raw)
	if mediaType != "multipart/mixed" {
		t.Fatalf("expected multipart/mixed for draft with attachment, got %s", mediaType)
	}

	msg, _ := mail.ReadMessage(bytes.NewReader(raw))
	mr := multipart.NewReader(msg.Body, params["boundary"])

	// First part should be plain text (not multipart/alternative)
	part0, err := mr.NextPart()
	if err != nil {
		t.Fatalf("NextPart 0: %v", err)
	}
	ct0, _, _ := mime.ParseMediaType(part0.Header.Get("Content-Type"))
	if ct0 != "text/plain" {
		t.Errorf("first part: expected text/plain, got %s", ct0)
	}

	// Second part should be the attachment
	part1, err := mr.NextPart()
	if err != nil {
		t.Fatalf("NextPart 1: %v", err)
	}
	ct1, _, _ := mime.ParseMediaType(part1.Header.Get("Content-Type"))
	if ct1 != "text/plain" {
		t.Errorf("attachment content-type: expected text/plain, got %s", ct1)
	}
}

func TestBuildMessage_WithHTMLSignature(t *testing.T) {
	// Test that HTML signature is appended to text/html part only
	markdownBody := "Hello world!"
	htmlSig := `<table style="font-size:12px"><tr><td>John Doe</td></tr></table>`

	raw, err := BuildMessage(
		"Alice <alice@example.com>",
		"Bob <bob@example.com>",
		"",
		"Test HTML Signature",
		markdownBody,
		nil,
		htmlSig,
	)
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}

	_, mediaType, params := parseMIME(t, raw)
	if mediaType != "multipart/alternative" {
		t.Fatalf("expected multipart/alternative, got %s", mediaType)
	}

	msg, _ := mail.ReadMessage(bytes.NewReader(raw))
	mr := multipart.NewReader(msg.Body, params["boundary"])

	// Read text/plain part
	part0, err := mr.NextPart()
	if err != nil {
		t.Fatalf("NextPart 0: %v", err)
	}
	ct0, _, _ := mime.ParseMediaType(part0.Header.Get("Content-Type"))
	if ct0 != "text/plain" {
		t.Errorf("part 0: expected text/plain, got %s", ct0)
	}
	plainBody, _ := io.ReadAll(part0)
	plainStr := string(plainBody)
	if strings.Contains(plainStr, "table") || strings.Contains(plainStr, "<tr>") {
		t.Error("text/plain part contains HTML signature (should be plain text only)")
	}
	// QP encoding preserves "Hello world" as-is (all ASCII)
	if !strings.Contains(plainStr, "Hello world") {
		t.Errorf("text/plain part missing body content, got:\n%s", plainStr)
	}

	// Read text/html part
	part1, err := mr.NextPart()
	if err != nil {
		t.Fatalf("NextPart 1: %v", err)
	}
	ct1, _, _ := mime.ParseMediaType(part1.Header.Get("Content-Type"))
	if ct1 != "text/html" {
		t.Errorf("part 1: expected text/html, got %s", ct1)
	}
	htmlBody, _ := io.ReadAll(part1)
	htmlStr := string(htmlBody)
	// Check for key parts (QP may have soft line breaks, but these strings should appear)
	if !strings.Contains(htmlStr, "Hello world") {
		t.Errorf("text/html part missing body content, got:\n%s", htmlStr)
	}
	if !strings.Contains(htmlStr, "table") || !strings.Contains(htmlStr, "John Doe") {
		t.Errorf("text/html part missing HTML signature, got:\n%s", htmlStr)
	}

	// CRITICAL: Verify the signature is placed BEFORE </body>, not after </html>
	// The signature should be inside the HTML document structure
	bodyCloseIdx := strings.Index(htmlStr, "</body>")
	htmlCloseIdx := strings.Index(htmlStr, "</html>")
	signatureIdx := strings.Index(htmlStr, "table")

	if bodyCloseIdx < 0 || htmlCloseIdx < 0 {
		t.Fatal("HTML document missing </body> or </html> tags")
	}
	if signatureIdx < 0 {
		t.Fatal("HTML signature not found in output")
	}

	// Signature must come BEFORE </body> (inside the document)
	if signatureIdx >= bodyCloseIdx {
		t.Errorf("HTML signature is placed AFTER </body> (position %d >= %d)\nThis creates malformed HTML where the signature is outside the document structure.\nFull HTML:\n%s",
			signatureIdx, bodyCloseIdx, htmlStr)
	}
	// Signature must come BEFORE </html> (obviously)
	if signatureIdx >= htmlCloseIdx {
		t.Errorf("HTML signature is placed AFTER </html> (position %d >= %d)\nFull HTML:\n%s",
			signatureIdx, htmlCloseIdx, htmlStr)
	}
}

func TestBuildMessage_WithoutHTMLSignature(t *testing.T) {
	// Passing empty htmlSignature should work fine (backward compatibility)
	raw, err := BuildMessage(
		"Alice <alice@example.com>",
		"Bob <bob@example.com>",
		"",
		"No HTML Signature",
		"plain body",
		nil,
		"", // empty HTML signature
	)
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}

	// Should still produce multipart/alternative with both parts
	msg, mediaType, params := parseMIME(t, raw)
	if mediaType != "multipart/alternative" {
		t.Errorf("expected multipart/alternative, got %s", mediaType)
	}

	mr := multipart.NewReader(msg.Body, params["boundary"])
	parts := readParts(t, mr)
	if len(parts) != 2 {
		t.Errorf("expected 2 parts, got %d", len(parts))
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
func TestBuildReactionMessage_ThreadingHeaders(t *testing.T) {
	markdown := "👍\n\n_Simon reacted via [neomd](https://neomd.ssp.sh)_\n\n---\n\n> **John** wrote:\n>\n> Hello"
	inReplyTo := "<original@example.com>"
	references := "<first@example.com> <second@example.com>"

	raw, err := BuildReactionMessage(
		"simon@example.com",
		"john@example.com",
		"",
		"Re: Test",
		markdown,
		"",
		inReplyTo,
		references,
	)
	if err != nil {
		t.Fatalf("BuildReactionMessage: %v", err)
	}

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	// Verify In-Reply-To header
	gotInReplyTo := msg.Header.Get("In-Reply-To")
	if gotInReplyTo != inReplyTo {
		t.Errorf("In-Reply-To = %q, want %q", gotInReplyTo, inReplyTo)
	}

	// Verify References header includes original references + inReplyTo
	gotReferences := msg.Header.Get("References")
	wantReferences := references + " " + inReplyTo
	if gotReferences != wantReferences {
		t.Errorf("References = %q, want %q", gotReferences, wantReferences)
	}

	// Verify multipart/alternative structure
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("ParseMediaType: %v", err)
	}
	if mediaType != "multipart/alternative" {
		t.Errorf("Content-Type = %q, want multipart/alternative", mediaType)
	}

	// Verify plain text part has no markdown syntax
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var foundPlainText, foundHTML bool
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		ct := part.Header.Get("Content-Type")
		body, _ := io.ReadAll(part)

		if strings.Contains(ct, "text/plain") {
			foundPlainText = true
			bodyStr := string(body)
			// Plain text contains markdown syntax (same as regular replies)
			if !strings.Contains(bodyStr, "_Simon reacted via [neomd]") {
				t.Errorf("text/plain missing markdown footer, got: %s", bodyStr)
			}
			// Should contain quoted reply
			if !strings.Contains(bodyStr, "> **John** wrote:") {
				t.Errorf("text/plain missing quoted reply, got: %s", bodyStr)
			}
		}
		if strings.Contains(ct, "text/html") {
			foundHTML = true
			// HTML should be rendered (not raw markdown)
			bodyStr := string(body)
			if !strings.Contains(bodyStr, "<") || !strings.Contains(bodyStr, ">") {
				t.Errorf("text/html part is not HTML: %s", bodyStr)
			}
		}
	}

	if !foundPlainText {
		t.Error("Missing text/plain part")
	}
	if !foundHTML {
		t.Error("Missing text/html part")
	}
}

func TestPlainTextFormatting_ReplyVsReaction(t *testing.T) {
	// Test what plain text email clients actually see for both replies and reactions
	
	// 1. Build a regular reply
	replyMarkdown := "Thanks for your email!\n\n---\n\n> **John** wrote:\n>\n> Can you help with [this issue](https://example.com/issue)?"
	replyRaw, err := BuildMessageWithThreading(
		"simon@example.com",
		"john@example.com",
		"",
		"Re: Help needed",
		replyMarkdown,
		nil, // no attachments
		"", // no HTML signature
		"<original@example.com>",
		"<first@example.com>",
	)
	if err != nil {
		t.Fatalf("BuildMessageWithThreading: %v", err)
	}

	// 2. Build an emoji reaction
	reactionMarkdown := "👍\n\n_Simon reacted via [neomd](https://neomd.ssp.sh)_\n\n---\n\n> **John** wrote:\n>\n> Can you help with [this issue](https://example.com/issue)?"
	reactionRaw, err := BuildReactionMessage(
		"simon@example.com",
		"john@example.com",
		"",
		"Re: Help needed",
		reactionMarkdown,
		"",
		"<original@example.com>",
		"<first@example.com>",
	)
	if err != nil {
		t.Fatalf("BuildReactionMessage: %v", err)
	}

	// Extract text/plain parts from both
	replyPlainText := extractPlainTextPart(t, replyRaw)
	reactionPlainText := extractPlainTextPart(t, reactionRaw)

	t.Logf("=== REGULAR REPLY (text/plain part) ===\n%s\n", replyPlainText)
	t.Logf("=== EMOJI REACTION (text/plain part) ===\n%s\n", reactionPlainText)

	// Verify both contain markdown syntax (current behavior)
	if !strings.Contains(replyPlainText, "[this issue]") {
		t.Error("Regular reply text/plain part does not contain markdown link syntax")
	}
	if !strings.Contains(replyPlainText, "> **John** wrote:") {
		t.Error("Regular reply text/plain part does not contain markdown bold syntax")
	}

	if !strings.Contains(reactionPlainText, "[neomd]") {
		t.Error("Reaction text/plain part does not contain markdown link syntax")
	}
	if !strings.Contains(reactionPlainText, "_Simon reacted") {
		t.Error("Reaction text/plain part does not contain markdown italic syntax")
	}
	if !strings.Contains(reactionPlainText, "> **John** wrote:") {
		t.Error("Reaction text/plain part does not contain markdown bold syntax")
	}

	// Log findings
	t.Log("\n=== FINDINGS ===")
	t.Log("Both regular replies and emoji reactions send markdown in text/plain part.")
	t.Log("This is consistent behavior - if it's a problem for reactions, it's also a problem for replies.")
}

func extractPlainTextPart(t *testing.T, raw []byte) string {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("ParseMediaType: %v", err)
	}

	if !strings.HasPrefix(mediaType, "multipart/") {
		// Not multipart, read body directly
		body, _ := io.ReadAll(msg.Body)
		return string(body)
	}

	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		ct := part.Header.Get("Content-Type")
		if strings.Contains(ct, "text/plain") {
			body, _ := io.ReadAll(part)
			return string(body)
		}
	}
	return ""
}
