package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/merge"
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

// --- Merge commands ---

func TestSplitCmdInput(t *testing.T) {
	cases := []struct{ in, word, args string }{
		{"merge Bounces from ACME", "merge", "Bounces from ACME"},
		{"merge   spaced  ", "merge", "spaced"},
		{"unmerge", "unmerge", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		w, a := splitCmdInput(c.in)
		if w != c.word || a != c.args {
			t.Errorf("splitCmdInput(%q) = %q,%q want %q,%q", c.in, w, a, c.word, c.args)
		}
	}
}

func TestMatchCmds_IgnoresArguments(t *testing.T) {
	if c := matchCmd("merge Bounces"); c == nil || c.name != "merge" {
		t.Fatalf("matchCmd with args should resolve merge, got %v", c)
	}
	if c := matchCmd("merge-sender X"); c == nil || c.name != "merge-sender" {
		t.Fatalf("got %v", c)
	}
}

func cmdModel(t *testing.T) Model {
	t.Helper()
	s, _ := merge.Load(filepath.Join(t.TempDir(), "merges.toml"))
	m := Model{merges: s, markedUIDs: map[uint32]bool{}, spyPixelKeys: map[string]bool{}, sortField: "date", sortReverse: true}
	m.inbox = newInboxList(100, 10, "", "")
	m.emails = []imap.Email{
		{UID: 1, MessageID: "<b1>", Subject: "Undelivered", From: "Mailer <mailer-daemon@x>", Date: time.Now().Add(-2 * time.Hour), Seen: true},
		{UID: 2, MessageID: "", Subject: "No id", From: "mailer-daemon@x", Date: time.Now().Add(-1 * time.Hour), Seen: true},
		{UID: 3, MessageID: "<k>", Subject: "Keep", From: "k@x", Date: time.Now(), Seen: true},
	}
	m.applyFilter()
	return m
}

func runCmd(t *testing.T, m Model, input string) Model {
	t.Helper()
	word, args := splitCmdInput(input)
	c := matchCmd(word)
	if c == nil {
		t.Fatalf("no command for %q", input)
	}
	var res tea.Model
	if c.runArgs != nil {
		res, _ = c.runArgs(&m, args)
	} else {
		res, _ = c.run(&m)
	}
	switch v := res.(type) {
	case *Model:
		return *v
	case Model:
		return v
	}
	t.Fatalf("unexpected result type %T", res)
	return m
}

func TestMergeCmd_MarkedEmailsSkipsMissingID(t *testing.T) {
	m := cmdModel(t)
	m.markedUIDs[1] = true
	m.markedUIDs[2] = true
	m = runCmd(t, m, "merge My Bounces")
	if title, ok := m.merges.TitleOf("<b1>"); !ok || title != "My Bounces" {
		t.Errorf("<b1> not merged: %q %v", title, ok)
	}
	if !strings.Contains(m.status, "1 skipped") {
		t.Errorf("status should report the skipped email without Message-ID, got %q", m.status)
	}
	if len(m.markedUIDs) != 0 {
		t.Error("marks should be cleared after :merge")
	}
	if n := len(m.inbox.Items()); n != 3 { // uid1 collapsed alone, uid2, uid3
		t.Errorf("items after merge = %d, want 3", n)
	}
}

func TestMergeCmd_RequiresTitle(t *testing.T) {
	m := cmdModel(t)
	m = runCmd(t, m, "merge")
	if !m.isError || !strings.Contains(m.status, "usage") {
		t.Errorf("empty title should be a usage error, got %q", m.status)
	}
}

func TestMergeSenderCmd_StoresRuleAndAppliesToLoaded(t *testing.T) {
	m := cmdModel(t)
	m.inbox.Select(2) // uid 1 (oldest, sorted last)
	if e := selectedEmail(m.inbox); e == nil || e.UID != 1 {
		t.Fatalf("cursor not on uid 1: %+v", e)
	}
	m = runCmd(t, m, "merge-sender Bounces")
	if _, ok := m.merges.MatchSender("MAILER-DAEMON@x"); !ok {
		t.Error("sender rule not stored")
	}
	if title, ok := m.merges.TitleOf("<b1>"); !ok || title != "Bounces" {
		t.Errorf("cursor email not merged: %q %v", title, ok)
	}
}

func TestUnmergeCmd_OnCollapsedRowAsksFirst(t *testing.T) {
	m := cmdModel(t)
	m.merges.Add("Bounces", "<b1>")
	m.applyFilter()
	m.inbox.Select(2)
	if it, ok := selectedItem(m.inbox); !ok || it.merge == nil {
		t.Fatalf("cursor should be on the collapsed row, got %+v", it)
	}
	m = runCmd(t, m, "unmerge")
	if m.pendingUnmerge != "Bounces" || !strings.Contains(m.status, "y/n") {
		t.Fatalf("expected y/n prompt, status=%q pending=%q", m.status, m.pendingUnmerge)
	}
	if _, ok := m.merges.TitleOf("<b1>"); !ok {
		t.Error("must not dissolve before confirmation")
	}
}

func TestUnmergeCmd_YDissolves(t *testing.T) {
	m := cmdModel(t)
	m.merges.Add("Bounces", "<b1>")
	m.applyFilter()
	m.inbox.Select(2)
	m = runCmd(t, m, "unmerge")
	if m.pendingUnmerge != "Bounces" {
		t.Fatalf("expected pending unmerge, got %q", m.pendingUnmerge)
	}
	res, _ := m.updateInbox(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = res.(Model)
	if _, ok := m.merges.TitleOf("<b1>"); ok {
		t.Error("y should dissolve the merge")
	}
	if m.pendingUnmerge != "" {
		t.Errorf("pendingUnmerge should be cleared, got %q", m.pendingUnmerge)
	}
	if n := len(m.inbox.Items()); n != 3 {
		t.Errorf("list should uncollapse after dissolve, got %d items", n)
	}
}

func TestUnmergeCmd_NCancels(t *testing.T) {
	m := cmdModel(t)
	m.merges.Add("Bounces", "<b1>")
	m.applyFilter()
	m.inbox.Select(2)
	m = runCmd(t, m, "unmerge")
	if m.pendingUnmerge != "Bounces" {
		t.Fatalf("expected pending unmerge, got %q", m.pendingUnmerge)
	}
	res, _ := m.updateInbox(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = res.(Model)
	if _, ok := m.merges.TitleOf("<b1>"); !ok {
		t.Error("n should not dissolve the merge")
	}
	if m.pendingUnmerge != "" {
		t.Errorf("pendingUnmerge should be cleared, got %q", m.pendingUnmerge)
	}
	if m.status != "Cancelled." {
		t.Errorf("status = %q, want %q", m.status, "Cancelled.")
	}
}

func TestMergeCmd_ViaEnterDispatch(t *testing.T) {
	m := cmdModel(t)
	m.inbox.Select(2) // uid 1 (oldest, sorted last)
	m.cmdMode = true
	m.cmdText = "merge Via Enter"
	res, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	switch v := res.(type) {
	case *Model:
		m = *v
	case Model:
		m = v
	default:
		t.Fatalf("unexpected result type %T", res)
	}
	if title, ok := m.merges.TitleOf("<b1>"); !ok || title != "Via Enter" {
		t.Errorf("enter should dispatch runArgs and merge, got title=%q ok=%v", title, ok)
	}
}

func TestUnmergeCmd_InsideMergeViewRemovesCursor(t *testing.T) {
	m := cmdModel(t)
	m.merges.Add("Bounces", "<b1>", "<zz>")
	m.offTabFolder = "Merged: Bounces"
	m.emails = m.emails[:1]
	m.applyFilter()
	m.inbox.Select(0)
	m = runCmd(t, m, "unmerge")
	if _, ok := m.merges.TitleOf("<b1>"); ok {
		t.Error("<b1> should be removed from the merge")
	}
	if _, ok := m.merges.TitleOf("<zz>"); !ok {
		t.Error("other member must stay")
	}
	if n := len(m.inbox.Items()); n != 0 {
		t.Errorf("removed email should leave the view, got %d items", n)
	}
}

func TestUnmergeCmd_OutsideMergeIsError(t *testing.T) {
	m := cmdModel(t)
	m.inbox.Select(0)
	m = runCmd(t, m, "unmerge")
	if !m.isError {
		t.Errorf("unmerge on a plain row should error, got %q", m.status)
	}
}

func TestTitleCompletions(t *testing.T) {
	m := cmdModel(t)
	m.merges.Add("Bounces", "<b1>")
	m.merges.Add("Newsletters", "<n1>")
	if got := m.titleCompletions("merge Bo"); len(got) != 1 || got[0] != "merge Bounces" {
		t.Errorf("got %v", got)
	}
	if got := m.titleCompletions("merge-sender "); len(got) != 2 {
		t.Errorf("empty prefix should list all titles, got %v", got)
	}
	if got := m.titleCompletions("reload x"); got != nil {
		t.Errorf("non-merge commands get no title completion, got %v", got)
	}
}
