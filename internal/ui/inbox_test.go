package ui

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/mattn/go-runewidth"
	"github.com/sspaeti/neomd/internal/imap"
)

// ansiRe strips CSI sequences added by lipgloss for colour/style so the
// remaining cell-width measurement reflects only visible glyphs.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSI(s string) string         { return ansiRe.ReplaceAllString(s, "") }
func runewidthStringWidth(s string) int { return runewidth.StringWidth(s) }

// renderRow renders a single emailItem via emailDelegate and returns the raw string.
func renderRow(item emailItem, width int) string {
	d := emailDelegate{}
	l := list.New([]list.Item{item}, d, width, 1)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)

	var buf bytes.Buffer
	d.Render(&buf, l, 0, item)
	return buf.String()
}

func TestReplyIndicator(t *testing.T) {
	base := imap.Email{
		UID:     1,
		From:    "Alice <alice@example.com>",
		Subject: "Hello",
		Date:    time.Now(),
		Seen:    true,
		Size:    1024,
	}

	t.Run("no reply indicator when not answered", func(t *testing.T) {
		e := base
		e.Answered = false
		row := renderRow(emailItem{email: e, index: 1}, 100)
		// The reply column should be a space, not ·
		if strings.Contains(row, "·") {
			t.Errorf("expected no reply indicator, got: %s", row)
		}
	})

	t.Run("reply indicator shown when answered", func(t *testing.T) {
		e := base
		e.Answered = true
		row := renderRow(emailItem{email: e, index: 1}, 100)
		if !strings.Contains(row, "·") {
			t.Errorf("expected · reply indicator, got: %s", row)
		}
	})
}

func TestReplyIndicatorWithThread(t *testing.T) {
	base := imap.Email{
		UID:     1,
		From:    "Bob <bob@example.com>",
		Subject: "test reply mode",
		Date:    time.Now(),
		Seen:    true,
		Size:    2048,
	}

	t.Run("reply dot with thread root", func(t *testing.T) {
		e := base
		e.Answered = true
		row := renderRow(emailItem{email: e, index: 2, threadPrefix: "╰"}, 100)
		if !strings.Contains(row, "·") {
			t.Errorf("expected · reply indicator with thread, got: %s", row)
		}
		if !strings.Contains(row, "╰") {
			t.Errorf("expected thread root prefix ╰, got: %s", row)
		}
		// · should appear before ╰
		dotIdx := strings.Index(row, "·")
		threadIdx := strings.Index(row, "╰")
		if dotIdx >= threadIdx {
			t.Errorf("expected · before ╰, dot at %d, thread at %d", dotIdx, threadIdx)
		}
	})

	t.Run("reply dot with thread continuation", func(t *testing.T) {
		e := base
		e.Answered = true
		row := renderRow(emailItem{email: e, index: 1, threadPrefix: "│"}, 100)
		if !strings.Contains(row, "·") {
			t.Errorf("expected · reply indicator, got: %s", row)
		}
		if !strings.Contains(row, "│") {
			t.Errorf("expected thread continuation │, got: %s", row)
		}
	})

	t.Run("no reply dot without answered in thread", func(t *testing.T) {
		e := base
		e.Answered = false
		row := renderRow(emailItem{email: e, index: 2, threadPrefix: "╰"}, 100)
		if strings.Contains(row, "·") {
			t.Errorf("expected no reply indicator, got: %s", row)
		}
		if !strings.Contains(row, "╰") {
			t.Errorf("expected thread root prefix ╰, got: %s", row)
		}
	})
}

func TestDisplaySafe(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"ascii passes through", "Hello, World!", "Hello, World!"},
		{"german umlauts pass through", "Grüße aus München, schön & groß", "Grüße aus München, schön & groß"},
		{"greek passes through", "Καλημέρα", "Καλημέρα"},
		{"cyrillic passes through", "Привет мир", "Привет мир"},
		{"bengali word collapses to dot", "আপনার", "·"},
		{"two bengali words separated by space", "আপনার দর্শকদের", "· ·"},
		{"japanese passes through", "こんにちは世界", "こんにちは世界"},
		{"korean passes through", "한국어 제목 테스트입니다", "한국어 제목 테스트입니다"},
		{"chinese passes through", "你好世界", "你好世界"},
		{"arabic two words", "مرحبا بالعالم", "· ·"},
		{"emoji collapses to single dot", "🚀🎉", "·"},
		{"mixed runs keep ascii context", "Re: আপনার and back to ASCII", "Re: · and back to ASCII"},
		{"trailing variation selector dropped", "werden︅", "werden"},
		{"long bengali subject — one dot per space-separated word", "Re: আপনার দর্শকদের জন্য একটি আকর্ষণীয় বিষয়বস্তু", "Re: · · · · · ·"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := displaySafe(tc.in)
			if got != tc.want {
				t.Errorf("displaySafe(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRowFitsTerminalWidth is the regression for the "bubbles list loses its
// top row when cursoring through a Bengali subject" bug. The invariant is
// that no row's rendered cell width exceeds the requested terminal width —
// if it does, the terminal soft-wraps and the list miscounts visible rows.
func TestRowFitsTerminalWidth(t *testing.T) {
	subjects := []string{
		"Plain ASCII subject",
		"Grüße aus München, schön & groß", // Latin-1
		"Re: আপনার দর্শকদের জন্য একটি আকর্ষণীয় বিষয়বস্তু", // Bengali (was breaking)
		"日本語のテストメールです件名サンプル",                                // Japanese
		"한국어 제목 테스트입니다",                                     // Korean
		"العربية موضوع البريد الإلكتروني",                   // Arabic
		"Zahlungsmethode muss aktualisiert werden︅",         // trailing variation selector
		"🚀 Mixed emoji and text 🎉",                          // emoji
	}
	widths := []int{80, 120, 190}
	for _, w := range widths {
		for _, subj := range subjects {
			row := renderRow(emailItem{
				email: imap.Email{
					UID:     1,
					From:    "Someone <someone@example.com>",
					Subject: subj,
					Date:    time.Now(),
					Seen:    true,
					Size:    1024,
				},
				index: 1,
			}, w)
			// Strip ANSI escapes before measuring — lipgloss adds them for colour.
			plain := stripANSI(row)
			if got := runewidthStringWidth(plain); got > w {
				t.Errorf("width=%d subject=%q: rendered row is %d cells (> %d)\n  row: %q",
					w, subj, got, w, plain)
			}
		}
	}
}

func TestSendDoneMsgUpdatesAnsweredFlag(t *testing.T) {
	// Simulate the local list update logic from the sendDoneMsg handler.
	emails := []imap.Email{
		{UID: 10, Subject: "unrelated", Answered: false},
		{UID: 20, Subject: "original", Answered: false},
		{UID: 30, Subject: "Re: original", Answered: false},
	}

	items := make([]list.Item, len(emails))
	for i, e := range emails {
		items[i] = emailItem{email: e, index: i + 1}
	}

	// Simulate the handler: mark UID 20 as Answered.
	replyToUID := uint32(20)
	for i, it := range items {
		if ei, ok := it.(emailItem); ok && ei.email.UID == replyToUID {
			ei.email.Answered = true
			items[i] = ei
			break
		}
	}

	// Verify only UID 20 was updated.
	for _, it := range items {
		ei := it.(emailItem)
		switch ei.email.UID {
		case 20:
			if !ei.email.Answered {
				t.Errorf("UID 20 should be Answered after send")
			}
		default:
			if ei.email.Answered {
				t.Errorf("UID %d should not be Answered", ei.email.UID)
			}
		}
	}
}

// Queued send-later messages get a display-only "[send-later …]" prefix;
// regular GTD mail in the same folder stays unmarked and the stored subject
// is never mutated.
func TestSendLaterPrefix(t *testing.T) {
	queued := imap.Email{Subject: "Ängebot", SendAt: time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)}
	if got := sendLaterPrefix(queued); !strings.HasPrefix(got, "[send-later ") {
		t.Errorf("queued prefix = %q", got)
	}
	if queued.Subject != "Ängebot" {
		t.Error("subject mutated")
	}
	if got := sendLaterPrefix(imap.Email{Subject: "GTD item"}); got != "" {
		t.Errorf("regular mail must have no prefix: %q", got)
	}
}

func TestRenderCollapsedMergeRow(t *testing.T) {
	newest := imap.Email{UID: 9, From: "Mailer-Daemon <mailer-daemon@x>", Subject: "Undelivered", Date: time.Now(), Seen: true, Size: 512}
	older := imap.Email{UID: 3, From: "Mailer-Daemon <mailer-daemon@x>", Subject: "Undelivered", Date: time.Now().Add(-48 * time.Hour), Seen: false, Answered: true, Size: 256}
	item := emailItem{
		email: newest, index: 1,
		merge: &mergeRow{title: "Bounces", members: []imap.Email{newest, older}},
	}
	row := renderRow(item, 100)
	for _, want := range []string{"≡", "Bounces (2)", "N", "·"} {
		if !strings.Contains(row, want) {
			t.Errorf("collapsed row missing %q: %s", want, row)
		}
	}
	if strings.Contains(row, "Undelivered") {
		t.Errorf("collapsed row should show the title, not a member subject: %s", row)
	}
}

func TestSetEmails_CollapsesMembers(t *testing.T) {
	emails := []imap.Email{
		{UID: 1, MessageID: "<b1>", Subject: "Undelivered", From: "d@x", Date: time.Now().Add(-2 * time.Hour), Seen: true},
		{UID: 2, MessageID: "<b2>", Subject: "Undelivered", From: "d@x", Date: time.Now().Add(-1 * time.Hour), Seen: true},
		{UID: 3, MessageID: "<k>", Subject: "Keep", From: "k@x", Date: time.Now(), Seen: true},
	}
	l := list.New(nil, emailDelegate{}, 100, 10)
	titleOf := func(id string) (string, bool) {
		if id == "<b1>" || id == "<b2>" {
			return "Bounces", true
		}
		return "", false
	}
	setEmails(&l, emails, map[uint32]bool{}, map[string]bool{}, false, "date", true, false, titleOf)
	if n := len(l.Items()); n != 2 {
		t.Fatalf("items = %d, want 2", n)
	}
	it := l.Items()[1].(emailItem)
	if it.merge == nil || it.merge.title != "Bounces" || it.index != 2 {
		t.Errorf("second item should be the Bounces merge with index 2, got %+v", it)
	}
	// Without titleOf nothing collapses.
	setEmails(&l, emails, map[uint32]bool{}, map[string]bool{}, false, "date", true, false, nil)
	if n := len(l.Items()); n != 3 {
		t.Errorf("items without titleOf = %d, want 3", n)
	}
}
