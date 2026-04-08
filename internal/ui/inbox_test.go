package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/sspaeti/neomd/internal/imap"
)

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
