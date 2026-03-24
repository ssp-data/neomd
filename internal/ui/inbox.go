package ui

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sspaeti/neomd/internal/imap"
)

// emailItem wraps imap.Email to satisfy bubbles/list.Item.
type emailItem struct {
	email imap.Email
}

func (e emailItem) FilterValue() string {
	return e.email.From + " " + e.email.Subject
}

func (e emailItem) Title() string       { return e.email.Subject }
func (e emailItem) Description() string { return e.email.From }

// emailDelegate is a custom list.ItemDelegate that renders one email per row.
type emailDelegate struct{}

func (d emailDelegate) Height() int                             { return 1 }
func (d emailDelegate) Spacing() int                           { return 0 }
func (d emailDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d emailDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	e, ok := item.(emailItem)
	if !ok {
		return
	}

	isSelected := index == m.Index()

	// Unread indicator
	indicator := "  "
	fromStyle := styleRead
	if !e.email.Seen {
		indicator = "● "
		fromStyle = styleUnread
	}

	// Truncate from and subject to fit terminal width
	width := m.Width()
	if width <= 0 {
		width = 80
	}
	dateStr := fmtDate(e.email.Date)
	dateWidth := len(dateStr) + 2
	fromMax := 25
	subjectMax := width - fromMax - dateWidth - 6
	if subjectMax < 10 {
		subjectMax = 10
	}

	from := truncate(e.email.From, fromMax)
	subject := truncate(e.email.Subject, subjectMax)

	row := fmt.Sprintf("%s%-*s  %-*s  %s",
		indicator,
		fromMax, from,
		subjectMax, subject,
		dateStr,
	)

	if isSelected {
		row = styleSelected.Render(row)
	} else {
		// Apply from style to the whole line (unread = brighter)
		_ = fromStyle // style applied via indicator colour above
		row = lipgloss.NewStyle().Foreground(colorText).Render(row)
		if !e.email.Seen {
			row = lipgloss.NewStyle().Foreground(colorUnread).Bold(true).Render(row)
		}
	}

	fmt.Fprint(w, row)
}

func fmtDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	if t.Year() == now.Year() {
		return t.Format("Jan 02")
	}
	return t.Format("2006")
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return s[:max-1] + "…"
}

// newInboxList creates a bubbles/list configured for the email inbox.
func newInboxList(width, height int) list.Model {
	l := list.New(nil, emailDelegate{}, width, height)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings()
	l.Styles.NoItems = styleStatus
	return l
}

// setEmails replaces the list contents.
func setEmails(l *list.Model, emails []imap.Email) tea.Cmd {
	items := make([]list.Item, len(emails))
	for i, e := range emails {
		items[i] = emailItem{email: e}
	}
	return l.SetItems(items)
}

// selectedEmail returns the currently highlighted email, or nil.
func selectedEmail(l list.Model) *imap.Email {
	item, ok := l.SelectedItem().(emailItem)
	if !ok {
		return nil
	}
	e := item.email
	return &e
}
