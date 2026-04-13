package imap

import (
	"context"
	"strings"
	"testing"

	imap "github.com/emersion/go-imap/v2"
)

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

	_, _, _, attachments, _ := parseBody([]byte(raw))

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

	_, _, _, attachments, _ := parseBody([]byte(raw))

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
	body1, _, _, _, _ := parseBody([]byte(draftMIME))

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

	body2, _, _, _, _ := parseBody([]byte(draftMIME2))

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

	body, _, _, _, _ := parseBody([]byte(regularMIME))

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

	_, _, _, _, references := parseBody([]byte(raw))

	wantReferences := "<msg1@example.com> <msg2@example.com>"
	if references != wantReferences {
		t.Errorf("References = %q, want %q", references, wantReferences)
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
