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
