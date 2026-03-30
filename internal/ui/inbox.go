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
	email  imap.Email
	index  int  // position in list (1-based)
	marked bool // selected for batch operation
}

func (e emailItem) FilterValue() string {
	return e.email.From + " " + e.email.Subject
}

func (e emailItem) Title() string       { return e.email.Subject }
func (e emailItem) Description() string { return e.email.From }

// emailDelegate is a custom list.ItemDelegate that renders one email per row.
type emailDelegate struct{}

func (d emailDelegate) Height() int                              { return 1 }
func (d emailDelegate) Spacing() int                            { return 0 }
func (d emailDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

// Column widths
const (
	colNumWidth    = 4 // "  1 "
	colFlagWidth   = 2 // "N " or "  "
	colDateWidth   = 7 // "Feb 03 "
	colAttachWidth = 2 // "@ " or "  "
	colSizeWidth   = 7 // "(38.2K)"
)

func (d emailDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	e, ok := item.(emailItem)
	if !ok {
		return
	}

	isSelected := index == m.Index()
	unread := !e.email.Seen

	width := m.Width()
	if width <= 0 {
		width = 80
	}

	// Fixed columns
	num := fmt.Sprintf("%3d ", e.index)
	// Flag column: mark takes priority; show unread alongside mark
	flag := "  "
	switch {
	case e.marked && !e.email.Seen:
		flag = "*N"
	case e.marked:
		flag = "* "
	case unread:
		flag = "N "
	}
	dateStr := fmtDate(e.email.Date) + " "
	attachStr := "  "
	if e.email.HasAttachment {
		attachStr = "@ "
	}
	sizeStr := fmtSize(e.email.Size)

	fixed := colNumWidth + colFlagWidth + colDateWidth + colAttachWidth + colSizeWidth + 2 // 2 spaces padding
	fromMax := 20
	subjectMax := width - fixed - fromMax - 2
	if subjectMax < 8 {
		subjectMax = 8
	}

	from := truncate(cleanFrom(e.email.From), fromMax)
	subject := truncate(e.email.Subject, subjectMax)

	if isSelected {
		row := fmt.Sprintf("%s%s%s%s%-*s  %-*s  %s",
			num, flag, dateStr, attachStr,
			fromMax, from,
			subjectMax, subject,
			sizeStr,
		)
		fmt.Fprint(w, styleSelected.Render(row))
		return
	}

	// Colorise each column separately
	numS := lipgloss.NewStyle().Foreground(colorNumber).Render(num)
	var flagS string
	switch {
	case e.marked:
		flagS = lipgloss.NewStyle().Foreground(colorDateCol).Bold(true).Render(flag)
	case unread:
		flagS = lipgloss.NewStyle().Foreground(colorAuthorUnread).Bold(true).Render(flag)
	default:
		flagS = lipgloss.NewStyle().Foreground(colorMuted).Render(flag)
	}
	dateS := lipgloss.NewStyle().Foreground(colorDateCol).Render(dateStr)
	attachS := lipgloss.NewStyle().Foreground(colorDateCol).Render(attachStr)

	fromStyle := lipgloss.NewStyle().Foreground(colorAuthorRead)
	subStyle := lipgloss.NewStyle().Foreground(colorSubjectRead)
	if unread {
		fromStyle = lipgloss.NewStyle().Foreground(colorAuthorUnread).Bold(true)
		subStyle = lipgloss.NewStyle().Foreground(colorSubjectUnread).Bold(true)
	}
	fromS := fromStyle.Render(fmt.Sprintf("%-*s", fromMax, from))
	subS := subStyle.Render(fmt.Sprintf("%-*s", subjectMax, subject))
	sizeS := lipgloss.NewStyle().Foreground(colorSizeCol).Render(sizeStr)

	fmt.Fprint(w, numS+flagS+dateS+attachS+fromS+"  "+subS+"  "+sizeS)
}

// cleanFrom strips the <addr> part when a display name is present.
func cleanFrom(from string) string {
	if i := strings.Index(from, " <"); i > 0 {
		return from[:i]
	}
	return from
}

// fmtSize formats a byte count into a compact "(38.2K)" string like neomutt.
func fmtSize(b uint32) string {
	switch {
	case b == 0:
		return "       "
	case b < 1024:
		return fmt.Sprintf("(%4dB)", b)
	case b < 1024*1024:
		return fmt.Sprintf("(%4.0fK)", float64(b)/1024)
	default:
		return fmt.Sprintf("(%4.1fM)", float64(b)/(1024*1024))
	}
}

func fmtDate(t time.Time) string {
	if t.IsZero() {
		return "      "
	}
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04 ")
	}
	if t.Year() == now.Year() {
		return t.Format("Jan 02")
	}
	return t.Format("Jan 06")
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 {
		return ""
	}
	// Count runes not bytes for proper unicode truncation
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

// newInboxList creates a bubbles/list configured for the email inbox.
func newInboxList(width, height int) list.Model {
	l := list.New(nil, emailDelegate{}, width, height)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false) // we manage filtering ourselves (filterText in Model)
	l.DisableQuitKeybindings()
	l.Styles.NoItems = styleStatus
	return l
}

// setEmails replaces the list contents, preserving marked state.
func setEmails(l *list.Model, emails []imap.Email, marked map[uint32]bool) tea.Cmd {
	items := make([]list.Item, len(emails))
	for i, e := range emails {
		items[i] = emailItem{email: e, index: i + 1, marked: marked[e.UID]}
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
