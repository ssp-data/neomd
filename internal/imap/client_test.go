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
