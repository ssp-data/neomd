package editor

import (
	"strings"
	"testing"
)

func TestParseHeaders(t *testing.T) {
	tests := []struct {
		name                                       string
		input                                      string
		wantTo, wantCC, wantBCC, wantFrom, wantSub string
		wantBodyContains                           string // substring the body must contain
		wantBodyNotContains                        string // substring the body must NOT contain
	}{
		{
			name: "all fields present",
			input: "# [neomd: to: alice@example.com]\n" +
				"# [neomd: cc: bob@example.com]\n" +
				"# [neomd: bcc: secret@example.com]\n" +
				"# [neomd: from: Me <me@example.com>]\n" +
				"# [neomd: subject: Hello World]\n" +
				"\n" +
				"Body text here.\n",
			wantTo:              "alice@example.com",
			wantCC:              "bob@example.com",
			wantBCC:             "secret@example.com",
			wantFrom:            "Me <me@example.com>",
			wantSub:             "Hello World",
			wantBodyContains:    "Body text here.",
			wantBodyNotContains: "neomd:",
		},
		{
			name: "missing cc and bcc",
			input: "# [neomd: to: alice@example.com]\n" +
				"# [neomd: subject: Only To]\n" +
				"\n" +
				"Some body.\n",
			wantTo:  "alice@example.com",
			wantCC:  "",
			wantBCC: "",
			wantSub: "Only To",
		},
		{
			name: "body preserved with newlines and markdown",
			input: "# [neomd: to: x@y.com]\n" +
				"# [neomd: subject: MD test]\n" +
				"\n" +
				"## Heading\n" +
				"\n" +
				"- bullet one\n" +
				"- bullet two\n" +
				"\n" +
				"Paragraph with **bold**.\n",
			wantTo:           "x@y.com",
			wantSub:          "MD test",
			wantBodyContains: "## Heading",
		},
		{
			name:                "no headers at all",
			input:               "Just plain text\nwith multiple lines.\n",
			wantTo:              "",
			wantCC:              "",
			wantBCC:             "",
			wantSub:             "",
			wantBodyContains:    "Just plain text",
			wantBodyNotContains: "",
		},
		{
			name: "case insensitive keys",
			input: "# [neomd: To: upper@example.com]\n" +
				"# [neomd: Subject: Case Test]\n" +
				"\n" +
				"body\n",
			wantTo:  "upper@example.com",
			wantSub: "Case Test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			to, cc, bcc, from, subject, body := ParseHeaders(tt.input)
			if to != tt.wantTo {
				t.Errorf("to = %q, want %q", to, tt.wantTo)
			}
			if cc != tt.wantCC {
				t.Errorf("cc = %q, want %q", cc, tt.wantCC)
			}
			if bcc != tt.wantBCC {
				t.Errorf("bcc = %q, want %q", bcc, tt.wantBCC)
			}
			if from != tt.wantFrom {
				t.Errorf("from = %q, want %q", from, tt.wantFrom)
			}
			if subject != tt.wantSub {
				t.Errorf("subject = %q, want %q", subject, tt.wantSub)
			}
			if tt.wantBodyContains != "" && !strings.Contains(body, tt.wantBodyContains) {
				t.Errorf("body missing %q, got:\n%s", tt.wantBodyContains, body)
			}
			if tt.wantBodyNotContains != "" && strings.Contains(body, tt.wantBodyNotContains) {
				t.Errorf("body should not contain %q, got:\n%s", tt.wantBodyNotContains, body)
			}
		})
	}
}

func TestPrelude(t *testing.T) {
	tests := []struct {
		name              string
		to, cc, bcc, from string
		subject           string
		signature         string
		wantHas           []string // substrings that must appear
		wantNot           []string // substrings that must NOT appear
	}{
		{
			name:    "basic without cc or sig",
			to:      "alice@example.com",
			subject: "Greetings",
			wantHas: []string{
				"# [neomd: to: alice@example.com]",
				"# [neomd: subject: Greetings]",
			},
			wantNot: []string{"# [neomd: cc:", "# [neomd: bcc:", "# [neomd: from:", "--  \n"},
		},
		{
			name:    "with cc",
			to:      "alice@example.com",
			cc:      "bob@example.com",
			subject: "Team",
			wantHas: []string{
				"# [neomd: to: alice@example.com]",
				"# [neomd: cc: bob@example.com]",
				"# [neomd: subject: Team]",
			},
		},
		{
			name:    "with bcc and from",
			to:      "alice@example.com",
			bcc:     "secret@example.com",
			from:    "Me <me@example.com>",
			subject: "Private",
			wantHas: []string{
				"# [neomd: bcc: secret@example.com]",
				"# [neomd: from: Me <me@example.com>]",
			},
		},
		{
			name:      "with signature",
			to:        "a@b.com",
			subject:   "Sig test",
			signature: "Best,\nAlice",
			wantHas:   []string{"--  \n", "Best,\nAlice"},
		},
		{
			name:    "without signature",
			to:      "a@b.com",
			subject: "No sig",
			wantNot: []string{"--  \n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Prelude(tt.to, tt.cc, tt.bcc, tt.from, tt.subject, tt.signature)
			for _, want := range tt.wantHas {
				if !strings.Contains(got, want) {
					t.Errorf("Prelude missing %q, got:\n%s", want, got)
				}
			}
			for _, notWant := range tt.wantNot {
				if strings.Contains(got, notWant) {
					t.Errorf("Prelude should not contain %q, got:\n%s", notWant, got)
				}
			}
		})
	}
}

func TestReplyPrelude(t *testing.T) {
	result := ReplyPrelude(
		"alice@example.com",
		"",
		"Re: Hello",
		"",
		"Bob Smith",
		"Line one\nLine two",
	)

	// Each original body line must be quoted with "> " prefix.
	if !strings.Contains(result, "> Line one") {
		t.Errorf("missing quoted line one, got:\n%s", result)
	}
	if !strings.Contains(result, "> Line two") {
		t.Errorf("missing quoted line two, got:\n%s", result)
	}

	// Attribution line includes original sender name.
	if !strings.Contains(result, "**Bob Smith** wrote:") {
		t.Errorf("missing attribution line, got:\n%s", result)
	}
}

func TestForwardPrelude(t *testing.T) {
	t.Run("adds Fwd prefix", func(t *testing.T) {
		result := ForwardPrelude("Hello", "", "Alice", "2024-01-01", "bob@x.com", "body")
		if !strings.Contains(result, "Fwd: Hello") {
			t.Errorf("expected Fwd: prefix, got:\n%s", result)
		}
	})

	t.Run("no double Fwd prefix", func(t *testing.T) {
		result := ForwardPrelude("Fwd: Hello", "", "Alice", "2024-01-01", "bob@x.com", "body")
		if strings.Contains(result, "Fwd: Fwd:") {
			t.Errorf("got double Fwd: prefix:\n%s", result)
		}
	})

	t.Run("case insensitive fwd check", func(t *testing.T) {
		result := ForwardPrelude("fwd: Hello", "", "Alice", "2024-01-01", "bob@x.com", "body")
		if strings.Contains(strings.ToLower(result), "fwd: fwd:") {
			t.Errorf("got double fwd: prefix:\n%s", result)
		}
	})

	t.Run("to field empty", func(t *testing.T) {
		result := ForwardPrelude("Hello", "", "Alice", "2024-01-01", "bob@x.com", "body")
		if !strings.Contains(result, "# [neomd: to: ]") {
			t.Errorf("to field should be empty, got:\n%s", result)
		}
	})

	t.Run("includes forward header block and body", func(t *testing.T) {
		result := ForwardPrelude("Hello", "", "Alice", "2024-01-01", "bob@x.com", "original text")
		if !strings.Contains(result, "---------- Forwarded message ----------") {
			t.Errorf("missing forward header block, got:\n%s", result)
		}
		if !strings.Contains(result, "From: Alice") {
			t.Errorf("missing From in forward header, got:\n%s", result)
		}
		if !strings.Contains(result, "> original text") {
			t.Errorf("missing quoted original body, got:\n%s", result)
		}
	})
}

func TestPreludeParseHeadersRoundTrip(t *testing.T) {
	tests := []struct {
		name              string
		to, cc, bcc, from string
		subject           string
	}{
		{
			name:    "to and subject only",
			to:      "alice@example.com",
			subject: "Round trip test",
		},
		{
			name:    "to cc and subject",
			to:      "alice@example.com",
			cc:      "bob@example.com",
			subject: "With CC",
		},
		{
			name:    "with bcc and from",
			to:      "alice@example.com",
			bcc:     "secret@example.com",
			from:    "Me <me@example.com>",
			subject: "With hidden recipients",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prelude := Prelude(tt.to, tt.cc, tt.bcc, tt.from, tt.subject, "")
			gotTo, gotCC, gotBCC, gotFrom, gotSubject, _ := ParseHeaders(prelude)
			if gotTo != tt.to {
				t.Errorf("round-trip to = %q, want %q", gotTo, tt.to)
			}
			if gotCC != tt.cc {
				t.Errorf("round-trip cc = %q, want %q", gotCC, tt.cc)
			}
			if gotBCC != tt.bcc {
				t.Errorf("round-trip bcc = %q, want %q", gotBCC, tt.bcc)
			}
			if gotFrom != tt.from {
				t.Errorf("round-trip from = %q, want %q", gotFrom, tt.from)
			}
			if gotSubject != tt.subject {
				t.Errorf("round-trip subject = %q, want %q", gotSubject, tt.subject)
			}
		})
	}
}
