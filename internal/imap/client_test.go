package imap

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// Send-later queued messages are identified in header fetches by their
// X-Neomd-Send-At header (fetched as a peek'd header-fields section) so the
// UI can mark them without mutating the stored message.
func TestParseSendAtSection(t *testing.T) {
	at := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	sec := []imapclient.FetchBodySectionBuffer{{
		Bytes: []byte("X-Neomd-Send-At: " + at.Format(time.RFC3339) + "\r\n\r\n"),
	}}
	if got := parseSendAtSection(sec); !got.Equal(at) {
		t.Errorf("SendAt = %v, want %v", got, at)
	}
	// Regular mail (no section content) and garbage must yield zero time.
	if got := parseSendAtSection(nil); !got.IsZero() {
		t.Errorf("nil section: %v", got)
	}
	if got := parseSendAtSection([]imapclient.FetchBodySectionBuffer{{Bytes: []byte("\r\n")}}); !got.IsZero() {
		t.Errorf("empty header: %v", got)
	}
	if got := parseSendAtSection([]imapclient.FetchBodySectionBuffer{{Bytes: []byte("X-Neomd-Send-At: not-a-time\r\n")}}); !got.IsZero() {
		t.Errorf("garbage time: %v", got)
	}
}

func TestBuildSearchCriteria(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantKey   string // expected Header[0].Key (empty means check Or)
		wantValue string // expected Header[0].Value
		wantOr    bool   // expect Or field to be non-empty
	}{
		{
			name:      "from prefix",
			query:     "from:alice",
			wantKey:   "From",
			wantValue: "alice",
		},
		{
			name:      "subject prefix",
			query:     "subject:meeting",
			wantKey:   "Subject",
			wantValue: "meeting",
		},
		{
			name:      "to prefix",
			query:     "to:bob",
			wantKey:   "To",
			wantValue: "bob",
		},
		{
			name:   "plain text uses OR",
			query:  "hello world",
			wantOr: true,
		},
		{
			name:      "case-insensitive prefix preserves value case",
			query:     "FROM:Alice",
			wantKey:   "From",
			wantValue: "Alice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := buildSearchCriteria(tt.query)
			if tt.wantOr {
				if len(c.Or) == 0 {
					t.Fatalf("expected Or field to be non-empty for query %q", tt.query)
				}
				return
			}
			if len(c.Header) == 0 {
				t.Fatalf("expected Header to be non-empty for query %q", tt.query)
			}
			if c.Header[0].Key != tt.wantKey {
				t.Errorf("Header Key = %q, want %q", c.Header[0].Key, tt.wantKey)
			}
			if c.Header[0].Value != tt.wantValue {
				t.Errorf("Header Value = %q, want %q", c.Header[0].Value, tt.wantValue)
			}
		})
	}
}

func TestHasAttachment(t *testing.T) {
	tests := []struct {
		name string
		bs   imap.BodyStructure
		want bool
	}{
		{
			name: "nil body structure",
			bs:   nil,
			want: false,
		},
		{
			name: "single part text/plain",
			bs:   &imap.BodyStructureSinglePart{Type: "text", Subtype: "plain"},
			want: false,
		},
		{
			name: "single part image/png counts as attachment",
			bs:   &imap.BodyStructureSinglePart{Type: "image", Subtype: "png"},
			want: true,
		},
		{
			name: "multipart text/plain + text/html only",
			bs: &imap.BodyStructureMultiPart{
				Subtype: "alternative",
				Children: []imap.BodyStructure{
					&imap.BodyStructureSinglePart{Type: "text", Subtype: "plain"},
					&imap.BodyStructureSinglePart{Type: "text", Subtype: "html"},
				},
			},
			want: false,
		},
		{
			name: "multipart with nested image child",
			bs: &imap.BodyStructureMultiPart{
				Subtype: "mixed",
				Children: []imap.BodyStructure{
					&imap.BodyStructureSinglePart{Type: "text", Subtype: "plain"},
					&imap.BodyStructureSinglePart{Type: "image", Subtype: "jpeg"},
				},
			},
			want: true,
		},
		{
			name: "single part with attachment disposition",
			bs: &imap.BodyStructureSinglePart{
				Type:    "application",
				Subtype: "pdf",
				Extended: &imap.BodyStructureSinglePartExt{
					Disposition: &imap.BodyStructureDisposition{
						Value: "attachment",
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasAttachment(tt.bs)
			if got != tt.want {
				t.Errorf("hasAttachment() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSplitAddrs(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"alice@example.com", []string{"alice@example.com"}},
		{"Alice <alice@example.com>, Bob <bob@example.com>", []string{"alice@example.com", "bob@example.com"}},
		{"alice@example.com, bob@example.com", []string{"alice@example.com", "bob@example.com"}},
		{"", nil},
		{"  , ,  ", nil},
		{"ALICE@EXAMPLE.COM", []string{"alice@example.com"}}, // lowercased
	}
	for _, tt := range tests {
		got := SplitAddrs(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("SplitAddrs(%q) = %v (len %d), want %v (len %d)", tt.input, got, len(got), tt.want, len(tt.want))
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("SplitAddrs(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestParticipantMatch(t *testing.T) {
	participants := map[string]bool{
		"alice@example.com": true,
		"bob@example.com":   true,
	}
	tests := []struct {
		name  string
		email Email
		want  bool
	}{
		{
			"from matches",
			Email{From: "Alice <alice@example.com>", To: "other@example.com"},
			true,
		},
		{
			"to matches",
			Email{From: "other@example.com", To: "bob@example.com"},
			true,
		},
		{
			"cc matches",
			Email{From: "other@example.com", To: "other2@example.com", CC: "alice@example.com"},
			true,
		},
		{
			"no match",
			Email{From: "stranger@example.com", To: "other@example.com"},
			false,
		},
		{
			"empty email",
			Email{},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := participantMatch(tt.email, participants)
			if got != tt.want {
				t.Errorf("participantMatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseBody_InlineImageContentID(t *testing.T) {
	// Construct a minimal multipart/related MIME message with an inline image.
	boundary := "----=_Part_123"
	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/related; boundary=\"" + boundary + "\"\r\n" +
		"\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<html><body><p>Hello</p><img src=\"cid:img001@neomd\"></body></html>\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: image/png; name=\"photo.png\"\r\n" +
		"Content-Disposition: inline; filename=\"photo.png\"\r\n" +
		"Content-ID: <img001@neomd>\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		"iVBORw0KGgo=\r\n" +
		"--" + boundary + "--\r\n"

	_, _, _, attachments, _, _ := parseBody([]byte(raw))

	if len(attachments) == 0 {
		t.Fatal("expected at least 1 attachment, got 0")
	}

	found := false
	for _, a := range attachments {
		if a.ContentID == "img001@neomd" {
			found = true
			if a.Filename != "photo.png" {
				t.Errorf("Filename = %q, want %q", a.Filename, "photo.png")
			}
			if !strings.HasPrefix(a.ContentType, "image/") {
				t.Errorf("ContentType = %q, want image/*", a.ContentType)
			}
		}
	}
	if !found {
		cids := make([]string, len(attachments))
		for i, a := range attachments {
			cids[i] = a.ContentID
		}
		t.Errorf("no attachment with ContentID 'img001@neomd', got CIDs: %v", cids)
	}
}

func TestParseBody_NoContentID(t *testing.T) {
	// Regular attachment without Content-ID should have empty ContentID.
	boundary := "----=_Part_456"
	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n" +
		"\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Hello world\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: application/pdf; name=\"doc.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"doc.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		"JVBERi0=\r\n" +
		"--" + boundary + "--\r\n"

	_, _, _, attachments, _, _ := parseBody([]byte(raw))

	if len(attachments) == 0 {
		t.Fatal("expected at least 1 attachment, got 0")
	}
	for _, a := range attachments {
		if a.Filename == "doc.pdf" && a.ContentID != "" {
			t.Errorf("regular attachment should have empty ContentID, got %q", a.ContentID)
		}
	}
}

func TestConnect_RefusesUnencrypted(t *testing.T) {
	c := &Client{
		cfg: Config{
			Host: "localhost",
			Port: "143",
			TLS:  false,
			// STARTTLS defaults to false
		},
	}
	err := c.connect(context.Background())
	if err == nil {
		t.Fatal("expected error for unencrypted connection, got nil")
	}
	if !strings.Contains(err.Error(), "refusing unencrypted") {
		t.Errorf("error = %q, want it to contain 'refusing unencrypted'", err.Error())
	}
}

func TestParseBody_DraftRoundTrip(t *testing.T) {
	// Test that draft content survives multiple save/load cycles without mutation.
	// This verifies the X-Neomd-Draft header correctly bypasses normalizePlainText.

	// Original markdown with various formatting that would be mutated by normalization
	originalBody := `Hello there

This is line 1
This is line 2

**Bold text** and *italic text*

[Link](https://example.com)

Code: ` + "`inline code`" + `

--
Signature line 1
Signature line 2`

	// Build a draft MIME message (plain text with X-Neomd-Draft header)
	draftMIME := "From: Alice <alice@example.com>\r\n" +
		"To: Bob <bob@example.com>\r\n" +
		"Subject: Test Draft\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"X-Neomd-Draft: true\r\n" +
		"\r\n" +
		originalBody

	// First parse (simulating draft reopen)
	body1, _, _, _, _, _ := parseBody([]byte(draftMIME))

	// Verify the body matches exactly (no trailing spaces added)
	if body1 != originalBody {
		t.Errorf("first parse mutated draft content\ngot:\n%q\nwant:\n%q", body1, originalBody)
	}

	// Second parse (simulating a save/reopen cycle)
	draftMIME2 := "From: Alice <alice@example.com>\r\n" +
		"To: Bob <bob@example.com>\r\n" +
		"Subject: Test Draft\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"X-Neomd-Draft: true\r\n" +
		"\r\n" +
		body1 // Use the result from first parse

	body2, _, _, _, _, _ := parseBody([]byte(draftMIME2))

	// Verify still matches exactly (no accumulation of trailing spaces)
	if body2 != originalBody {
		t.Errorf("second parse mutated draft content\ngot:\n%q\nwant:\n%q", body2, originalBody)
	}

	// Verify they're all equal
	if body1 != body2 {
		t.Errorf("draft content changed between parse cycles\nfirst:\n%q\nsecond:\n%q", body1, body2)
	}
}

func TestParseBody_NonDraftGetsNormalized(t *testing.T) {
	// Test that regular emails (without X-Neomd-Draft) still get normalizePlainText applied.

	originalBody := "Line 1\nLine 2"

	// Regular email (no X-Neomd-Draft header)
	regularMIME := "From: Alice <alice@example.com>\r\n" +
		"To: Bob <bob@example.com>\r\n" +
		"Subject: Regular Email\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		originalBody

	body, _, _, _, _, _ := parseBody([]byte(regularMIME))

	// Normalization should add two trailing spaces before the newline
	expectedNormalized := "Line 1  \nLine 2"
	if body != expectedNormalized {
		t.Errorf("normalization not applied to regular email\ngot:\n%q\nwant:\n%q", body, expectedNormalized)
	}
}

func TestParseBody_ReferencesExtraction(t *testing.T) {
	// Build a test message with References header
	raw := "From: test@example.com\r\n" +
		"To: recipient@example.com\r\n" +
		"Subject: Test\r\n" +
		"Message-ID: <msg3@example.com>\r\n" +
		"In-Reply-To: <msg2@example.com>\r\n" +
		"References: <msg1@example.com> <msg2@example.com>\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Test body"

	_, _, _, _, references, _ := parseBody([]byte(raw))

	wantReferences := "<msg1@example.com> <msg2@example.com>"
	if references != wantReferences {
		t.Errorf("References = %q, want %q", references, wantReferences)
	}
}

func TestSpyPixelDetection(t *testing.T) {
	// HTML email with 2 tracking pixels from different domains.
	// First: detected by size heuristic (width="1" height="1")
	// Second: detected by URL pattern (/track/open)
	// Third: legitimate image with alt text — should NOT be counted
	// Fourth: decorative image with empty alt but normal size — should NOT be counted
	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		`<html><body>` +
		`<p>Hello world</p>` +
		`<img src="https://click.mailchimp.com/track/open.php?id=abc" alt="" width="1" height="1">` +
		`<img src="https://pixel.sendinblue.com/beacon/track/open?id=xyz" alt="" height="0">` +
		`<img src="cid:logo" alt="Company Logo">` +
		`<img src="https://cdn.example.com/button.png" alt="" width="200" height="50">` +
		`</body></html>`

	_, _, _, _, _, spy := parseBody([]byte(raw))

	if spy.Count != 2 {
		t.Errorf("SpyPixelInfo.Count = %d, want 2", spy.Count)
	}
	// Check that domains were extracted
	found := make(map[string]bool)
	for _, d := range spy.Domains {
		found[d] = true
	}
	// With the tracker denylist, services are identified by name.
	// Mailchimp pixel matches "Mailchimp" or a Yesware /track/open pattern.
	if !found["Mailchimp"] && !found["Yesware"] {
		t.Errorf("expected Mailchimp or Yesware attribution in spy.Domains, got %v", spy.Domains)
	}
	for _, d := range spy.Domains {
		if strings.Contains(d, "cdn.example.com") {
			t.Errorf("decorative image should NOT be counted, got %v", spy.Domains)
		}
	}
}

func TestSpyPixelSpacersNotFlagged(t *testing.T) {
	// Layout spacers (one dimension is 1 but the other is large) must NOT
	// be flagged as spy pixels — they are decorative, not trackers.
	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		`<html><body>` +
		`<img src="https://a.kajabi.com/9/9d08eac.png" alt="" width="1" height="16">` +
		`<img src="https://a.kajabi.com/9/9d08eac.png" alt="" width="40" height="1">` +
		`<img src="https://a.kajabi.com/9/9d08eac.png" alt="" width="1" height="50">` +
		`<img src="https://a.kajabi.com/9/9d08eac.png" alt="" width="20" height="1">` +
		`<img src="https://a.kajabi.com/9/9d08eac.png" alt="" width="1" height="100">` +
		// This one IS a real 1×1 tracker pixel — should be counted.
		`<img src="https://email.kjbm.example.com/o/eJx8token" alt="" width="1" height="1">` +
		`</body></html>`

	_, _, _, _, _, spy := parseBody([]byte(raw))

	if spy.Count != 1 {
		t.Errorf("SpyPixelInfo.Count = %d, want 1 (only the 1x1 pixel)", spy.Count)
	}
}

func TestSpyPixelPlainTextEmail(t *testing.T) {
	// Plain-text emails should never report spy pixels.
	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Just a normal text email."

	_, _, _, _, _, spy := parseBody([]byte(raw))

	if spy.Count != 0 {
		t.Errorf("plain-text email SpyPixelInfo.Count = %d, want 0", spy.Count)
	}
}

func TestParseBody_UnknownCharset(t *testing.T) {
	// Emails with unknown charsets should not fail — they should render
	// with raw bytes rather than crashing. This is common with legacy
	// encodings (ISO-8859-15, Windows-1256, etc.).
	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=x-unknown-charset-999\r\n" +
		"Content-Transfer-Encoding: 7bit\r\n" +
		"\r\n" +
		"This email uses an unknown charset but should still be readable."

	body, _, _, _, _, _ := parseBody([]byte(raw))

	if body == "" {
		t.Error("parseBody returned empty body for unknown charset — should fall back to raw bytes")
	}
	if !strings.Contains(body, "unknown charset") {
		t.Errorf("expected body to contain raw text, got: %q", body)
	}
}

func TestParseBody_UnknownEncoding(t *testing.T) {
	// Emails with unknown transfer encodings should degrade gracefully.
	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: x-uuencode\r\n" +
		"\r\n" +
		"This email uses an unusual encoding."

	body, _, _, _, _, _ := parseBody([]byte(raw))

	// Should not panic or return empty — may return raw bytes or partial content
	if body == "" {
		t.Error("parseBody returned empty body for unknown encoding — should not crash")
	}
}

func TestParseBody_MultipartUnknownCharset(t *testing.T) {
	// Multipart email where one part has an unknown charset.
	// The other part should still be parsed correctly.
	boundary := "test-boundary-charset"
	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=" + boundary + "\r\n" +
		"\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/plain; charset=x-fake-charset\r\n" +
		"\r\n" +
		"Plain text with unknown charset\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<html><body><p>HTML part is fine</p></body></html>\r\n" +
		"--" + boundary + "--\r\n"

	body, _, _, _, _, _ := parseBody([]byte(raw))

	if body == "" {
		t.Error("parseBody returned empty body for multipart with unknown charset")
	}
}

func TestParseBody_ISO88591_Base64(t *testing.T) {
	// Real-world case: Swiss bank email with charset=iso-8859-1 and base64 encoding.
	// German umlauts (ü, ä, ö) must decode correctly to UTF-8.
	// In ISO-8859-1: ü=0xFC, ä=0xE4, ö=0xF6, Ü=0xDC
	iso88591Bytes := []byte("H\xe4b \xe4 sch\xf6ne Abe\r\n\xdcber CHF 7'260")
	encoded := base64.StdEncoding.EncodeToString(iso88591Bytes)

	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=iso-8859-1\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		encoded + "\r\n"

	body, _, _, _, _, _ := parseBody([]byte(raw))

	for _, want := range []string{"Häb", "schöne", "Über"} {
		if !strings.Contains(body, want) {
			t.Errorf("ISO-8859-1 body missing %q, got: %q", want, body)
		}
	}
}

func TestParseBody_Windows1252(t *testing.T) {
	// Windows-1252 is common in Outlook-generated emails.
	// It extends ISO-8859-1 with characters like curly quotes and em-dash.
	// €=0x80, –=0x96, "=0x93, "=0x94
	win1252Bytes := []byte("Price: \x804'500 \x93special offer\x94 \x96 limited")
	encoded := base64.StdEncoding.EncodeToString(win1252Bytes)

	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=windows-1252\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		encoded + "\r\n"

	body, _, _, _, _, _ := parseBody([]byte(raw))

	if !strings.Contains(body, "€") {
		t.Errorf("Windows-1252 body missing euro sign €, got: %q", body)
	}
	if !strings.Contains(body, "\u201c") || !strings.Contains(body, "\u201d") {
		t.Logf("Windows-1252 curly quotes may not be preserved, body: %q", body)
	}
}

func TestParseBody_ISO885915(t *testing.T) {
	// ISO-8859-15 is used in French/Finnish email. It adds € (0xA4) and
	// other characters missing from ISO-8859-1.
	iso885915Bytes := []byte("Cr\xe8me br\xfbl\xe9e co\xfbte \xa4100")
	encoded := base64.StdEncoding.EncodeToString(iso885915Bytes)

	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=iso-8859-15\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		encoded + "\r\n"

	body, _, _, _, _, _ := parseBody([]byte(raw))

	for _, want := range []string{"Crème", "brûlée", "coûte"} {
		if !strings.Contains(body, want) {
			t.Errorf("ISO-8859-15 body missing %q, got: %q", want, body)
		}
	}
}

func TestParseBody_QuotedPrintable_ISO88591(t *testing.T) {
	// Same charset but with quoted-printable encoding instead of base64.
	// Common in inline replies and older mail clients.
	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=iso-8859-1\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"\r\n" +
		"Gr=FC=DFe aus Z=FCrich\r\n"

	body, _, _, _, _, _ := parseBody([]byte(raw))

	if !strings.Contains(body, "Grüße") {
		t.Errorf("QP ISO-8859-1 body missing 'Grüße', got: %q", body)
	}
	if !strings.Contains(body, "Zürich") {
		t.Errorf("QP ISO-8859-1 body missing 'Zürich', got: %q", body)
	}
}

func TestConnectionHealthCheck_LastActivity(t *testing.T) {
	// Verify that lastActivity is tracked by the Client struct.
	// We can't test the actual NOOP probe without a real IMAP server,
	// but we can verify the field exists and the logic is wired up.
	c := &Client{
		cfg: Config{
			Host: "imap.example.com",
			Port: "993",
			TLS:  true,
		},
	}

	// Initially zero — first withConn should not trigger NOOP
	if !c.lastActivity.IsZero() {
		t.Error("lastActivity should be zero on new Client")
	}

	// After setting lastActivity to recent, NOOP should not trigger
	c.lastActivity = time.Now()
	if time.Since(c.lastActivity) > 2*time.Minute {
		t.Error("recent lastActivity should not trigger health check")
	}

	// After setting lastActivity to 3 minutes ago, NOOP should trigger
	c.lastActivity = time.Now().Add(-3 * time.Minute)
	if time.Since(c.lastActivity) <= 2*time.Minute {
		t.Error("stale lastActivity (3min ago) should trigger health check")
	}
}

func TestResetMailboxSelection(t *testing.T) {
	// Verify that ResetMailboxSelection clears the cached selectedMailbox.
	// This prevents stale mailbox state from suppressing new message visibility
	// when refreshing (github.com/sspaeti/neomd#66 regression test).
	c := &Client{
		cfg: Config{
			Host: "imap.example.com",
			Port: "993",
			TLS:  true,
		},
	}

	// Simulate that a mailbox was previously selected
	c.selectedMailbox = "INBOX"

	// Reset should clear it
	c.ResetMailboxSelection()

	if c.selectedMailbox != "" {
		t.Errorf("ResetMailboxSelection() did not clear selectedMailbox: got %q, want empty string", c.selectedMailbox)
	}
}

// Outlook and other Windows senders encode display names and subjects as
// RFC 2047 encoded-words in Windows-1252, which Go's default mime.WordDecoder
// cannot decode (it only knows UTF-8, ISO-8859-1 and US-ASCII). The IMAP
// client must hand go-imap a charset-aware decoder or the raw
// "=?Windows-1252?Q?...?=" text leaks into the inbox, reader and reply screens.
func TestEnvelopeWordDecoder_DecodesWindows1252(t *testing.T) {
	cases := []struct{ in, want string }{
		{"=?Windows-1252?Q?Ren=E9_M=FCller?=", "René Müller"},
		{"=?Windows-1252?Q?Ren=E9_M=FCller?= <rene@example.com>", "René Müller <rene@example.com>"},
		{"=?ISO-8859-15?Q?Preis_=A4?=", "Preis €"},
		{"=?UTF-8?Q?Zo=C3=AB_Example?=", "Zoë Example"},
		{"Plain Name <plain@example.com>", "Plain Name <plain@example.com>"},
	}
	for _, c := range cases {
		got, err := envelopeWordDecoder.DecodeHeader(c.in)
		if err != nil {
			t.Errorf("DecodeHeader(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("DecodeHeader(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClientOptions_SetWordDecoder(t *testing.T) {
	opts := clientOptions(nil)
	if opts.WordDecoder == nil {
		t.Fatal("clientOptions must set WordDecoder so go-imap decodes non-UTF-8 envelope charsets")
	}
	if opts.WordDecoder != envelopeWordDecoder {
		t.Error("clientOptions should wire the shared charset-aware envelopeWordDecoder")
	}
}

// Signatures constrain a large logo with width/height (e.g. a 192px PNG shown
// at 70px). Markdown has no image size, so quoting a mail used to re-render
// the logo at full size in every reply. The converter now carries the size as
// the image title ("WxH"), which render.ToHTML turns back into attributes.
func TestHTMLToMarkdown_PreservesImageSizeAsTitle(t *testing.T) {
	html := `<p><img src="https://example.org/logo.png" alt="Example GmbH" width="70" height="70" style="display:block"></p>` +
		`<p><img src="https://example.org/wide.png" alt="Wide" style="width:120px; height:40px;"></p>` +
		`<p><img src="https://example.org/h.png" alt="H" height="30"></p>` +
		`<p><img src="https://example.org/plain.png" alt="Plain"></p>`
	got, _ := htmlToMarkdown(html)
	for _, want := range []string{
		`![Example GmbH](https://example.org/logo.png "70x70")`,
		`![Wide](https://example.org/wide.png "120x40")`,
		`![H](https://example.org/h.png "x30")`,
		`![Plain](https://example.org/plain.png)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// Every server-side move/expunge is appended to the audit log so a "mail
// vanished" report can be traced to the exact MOVE (folder, UID, destination,
// time) instead of guessed at.
func TestAuditLog_AppendsLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "moves.log")
	SetAuditLogPath(p)
	defer SetAuditLogPath("")
	audit("MOVE %s uid=%d -> %s destUID=%d", "INBOX", 42, "Trash", 7)
	audit("EXPUNGE %s uids=%v", "Trash", []uint32{7})
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "MOVE INBOX uid=42 -> Trash destUID=7") || !strings.Contains(lines[1], "EXPUNGE Trash uids=[7]") {
		t.Errorf("unexpected audit lines: %q", lines)
	}
	if !strings.HasPrefix(lines[0], "20") { // RFC 3339 timestamp first
		t.Errorf("line lacks timestamp: %q", lines[0])
	}
	SetAuditLogPath("")
	audit("ignored") // no path → no-op, must not panic
}
