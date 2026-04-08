package ui

import (
	"strings"
	"testing"

	"github.com/sspaeti/neomd/internal/imap"
)

func TestMatchCmds_EmptyReturnsAll(t *testing.T) {
	got := matchCmds("")
	if len(got) != len(cmdRegistry) {
		t.Fatalf("matchCmds(\"\") returned %d commands, want %d", len(got), len(cmdRegistry))
	}
}

func TestMatchCmds_ExactName(t *testing.T) {
	got := matchCmds("screen")
	names := make([]string, len(got))
	for i, c := range got {
		names[i] = c.name
	}
	if len(got) != 2 {
		t.Fatalf("matchCmds(\"screen\") = %v, want [screen, screen-all]", names)
	}
	// Both "screen" and "screen-all" should match.
	found := map[string]bool{}
	for _, c := range got {
		found[c.name] = true
	}
	for _, want := range []string{"screen", "screen-all"} {
		if !found[want] {
			t.Errorf("expected %q in results, got %v", want, names)
		}
	}
}

func TestMatchCmds_Alias(t *testing.T) {
	got := matchCmds("sa")
	if len(got) == 0 {
		t.Fatal("matchCmds(\"sa\") returned no matches, want screen-all via alias")
	}
	found := false
	for _, c := range got {
		if c.name == "screen-all" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected screen-all in results for alias \"sa\"")
	}
}

func TestMatchCmds_Prefix(t *testing.T) {
	got := matchCmds("sc")
	if len(got) == 0 {
		t.Fatal("matchCmds(\"sc\") returned no matches")
	}
	for _, c := range got {
		if !strings.HasPrefix(c.name, "sc") {
			// Check aliases too.
			aliasMatch := false
			for _, a := range c.aliases {
				if strings.HasPrefix(a, "sc") {
					aliasMatch = true
					break
				}
			}
			if !aliasMatch {
				t.Errorf("unexpected match %q for prefix \"sc\"", c.name)
			}
		}
	}
}

func TestMatchCmds_NoMatch(t *testing.T) {
	got := matchCmds("zzz")
	if len(got) != 0 {
		t.Fatalf("matchCmds(\"zzz\") returned %d matches, want 0", len(got))
	}
}

func TestMatchCmd_FirstMatch(t *testing.T) {
	got := matchCmd("r")
	if got == nil {
		t.Fatal("matchCmd(\"r\") returned nil, want non-nil")
	}
}

func TestMatchCmd_Empty(t *testing.T) {
	got := matchCmd("")
	if got != nil {
		t.Fatalf("matchCmd(\"\") = %q, want nil", got.name)
	}
}

func TestScreenSummary(t *testing.T) {
	moves := []autoScreenMove{
		{email: &imap.Email{UID: 1}, dst: "Archive"},
		{email: &imap.Email{UID: 2}, dst: "Archive"},
		{email: &imap.Email{UID: 3}, dst: "Spam"},
		{email: &imap.Email{UID: 4}, dst: "Archive"},
		{email: &imap.Email{UID: 5}, dst: "Trash"},
	}
	got := screenSummary(moves)

	// Should mention total count.
	if !strings.Contains(got, "5") {
		t.Errorf("summary should contain total count 5, got: %s", got)
	}

	// Should mention each destination folder.
	for _, folder := range []string{"Archive", "Spam", "Trash"} {
		if !strings.Contains(got, folder) {
			t.Errorf("summary should mention folder %q, got: %s", folder, got)
		}
	}

	// Should contain the arrow notation for counts.
	if !strings.Contains(got, "3→Archive") {
		t.Errorf("summary should contain \"3→Archive\", got: %s", got)
	}
	if !strings.Contains(got, "1→Spam") {
		t.Errorf("summary should contain \"1→Spam\", got: %s", got)
	}
	if !strings.Contains(got, "1→Trash") {
		t.Errorf("summary should contain \"1→Trash\", got: %s", got)
	}
}

// --- Thread / Conversation tests ---

func TestNormalizeSubject(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Re: Hello World", "hello world"},
		{"Fwd: Re: Hello World", "hello world"},
		{"AW: RE: FW: Meeting notes", "meeting notes"},
		{"Hello World", "hello world"},
		{"Re: Re: Re: Deep thread", "deep thread"},
		{"", ""},
		{"Re[2]: Numbered reply", "numbered reply"},
		{"  Re:  Whitespace  ", "whitespace"},
	}
	for _, tt := range tests {
		got := normalizeSubject(tt.input)
		if got != tt.want {
			t.Errorf("normalizeSubject(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestHasReplyPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"Re: Hello", true},
		{"Fwd: Hello", true},
		{"AW: Hello", true},
		{"Hello", false},
		{"", false},
		{"RE: caps", true},
		{"Fw: short form", true},
	}
	for _, tt := range tests {
		got := hasReplyPrefix(tt.input)
		if got != tt.want {
			t.Errorf("hasReplyPrefix(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
