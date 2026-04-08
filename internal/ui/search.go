package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sspaeti/neomd/internal/imap"
)

// ── IMAP server-side search ──────────────────────────────────────────────
//
// Local filter (/) searches loaded emails in the current folder in-memory.
//
// IMAP search (space + /) queries ALL emails across ALL folders on the server
// using IMAP SEARCH. Results are displayed in a temporary "Search" tab
// (like Spam or Drafts).
//
// Query syntax:
//   plain text   — matches FROM, SUBJECT, or TO (any field)
//   from:value   — matches FROM header only
//   subject:val  — matches SUBJECT header only
//   to:value     — matches TO header only

// imapSearchResultMsg carries results from a server-side IMAP search.
type imapSearchResultMsg struct {
	emails []imap.Email
	query  string
	err    error
}

// imapSearchAllCmd runs IMAP SEARCH across all configured folders.
func (m Model) imapSearchAllCmd(query string) tea.Cmd {
	cli := m.imapCli()
	f := m.cfg.Folders
	folders := []string{
		f.Inbox, f.Sent, f.Trash, f.Drafts,
		f.ToScreen, f.Feed, f.PaperTrail, f.ScreenedOut,
		f.Archive, f.Waiting, f.Scheduled, f.Someday, f.Spam,
	}
	if f.Work != "" {
		folders = append(folders, f.Work)
	}
	return func() tea.Msg {
		emails, err := cli.SearchAllFolders(nil, folders, query)
		return imapSearchResultMsg{emails: emails, query: query, err: err}
	}
}

// updateIMAPSearch handles key input while the IMAP search prompt is active.
// Returns true if the key was consumed.
func (m *Model) updateIMAPSearch(key string) (tea.Model, tea.Cmd, bool) {
	if !m.imapSearchActive {
		return m, nil, false
	}

	switch key {
	case "esc":
		m.imapSearchActive = false
		m.imapSearchText = ""
		return m, nil, true

	case "enter":
		query := strings.TrimSpace(m.imapSearchText)
		if query == "" {
			m.imapSearchActive = false
			return m, nil, true
		}
		m.imapSearchActive = false
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.imapSearchAllCmd(query)), true

	case "backspace", "ctrl+h":
		runes := []rune(m.imapSearchText)
		if len(runes) > 0 {
			m.imapSearchText = string(runes[:len(runes)-1])
		}
		return m, nil, true

	default:
		if len(key) == 1 {
			m.imapSearchText += key
			return m, nil, true
		}
	}
	return m, nil, true
}

// handleIMAPSearchResult processes the result of an IMAP SEARCH command.
// Displays results in a temporary "Search" off-tab.
func (m *Model) handleIMAPSearchResult(msg imapSearchResultMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.status = "Search error: " + msg.err.Error()
		m.isError = true
		return m, nil
	}
	if len(msg.emails) == 0 {
		m.status = fmt.Sprintf("No results for %q.", msg.query)
		return m, nil
	}
	m.imapSearchResults = true
	m.offTabFolder = "Search"
	// Prepend folder name to subject so user can see where each result is from
	for i := range msg.emails {
		folder := msg.emails[i].Folder
		msg.emails[i].Subject = "[" + folder + "] " + msg.emails[i].Subject
	}
	m.emails = msg.emails
	m.markedUIDs = make(map[uint32]bool)
	m.filterActive = false
	m.filterText = ""
	m.status = fmt.Sprintf("Found %d email(s) for %q — esc to return, enter to open", len(msg.emails), msg.query)
	return m, m.sortEmails()
}

// everythingResultMsg carries results from fetching latest across all folders.
type everythingResultMsg struct {
	emails []imap.Email
	err    error
}

// fetchEverythingCmd fetches the latest N emails across all folders.
func (m Model) fetchEverythingCmd() tea.Cmd {
	cli := m.imapCli()
	f := m.cfg.Folders
	folders := []string{
		f.Inbox, f.Sent, f.Trash, f.Drafts,
		f.ToScreen, f.Feed, f.PaperTrail, f.ScreenedOut,
		f.Archive, f.Waiting, f.Scheduled, f.Someday, f.Spam,
	}
	if f.Work != "" {
		folders = append(folders, f.Work)
	}
	return func() tea.Msg {
		emails, err := cli.FetchLatestAllFolders(nil, folders, 50)
		return everythingResultMsg{emails: emails, err: err}
	}
}

// handleEverythingResult displays the "Everything" view.
func (m *Model) handleEverythingResult(msg everythingResultMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.status = "Everything: " + msg.err.Error()
		m.isError = true
		return m, nil
	}
	if len(msg.emails) == 0 {
		m.status = "No emails found."
		return m, nil
	}
	m.offTabFolder = "Everything"
	// Prepend folder name so user knows where each email is
	for i := range msg.emails {
		msg.emails[i].Subject = "[" + msg.emails[i].Folder + "] " + msg.emails[i].Subject
	}
	m.emails = msg.emails
	m.markedUIDs = make(map[uint32]bool)
	m.filterActive = false
	m.filterText = ""
	m.status = fmt.Sprintf("Everything — %d most recent emails across all folders. esc to close.", len(msg.emails))
	return m, m.sortEmails()
}

// viewIMAPSearchBar renders the search prompt at the bottom of the inbox.
func (m Model) viewIMAPSearchBar() string {
	cursor := ""
	if m.imapSearchActive {
		cursor = "█"
	}
	if m.imapSearchResults && !m.imapSearchActive {
		return styleHelp.Render(fmt.Sprintf("  search: %q — esc to close · from: subject: to: prefixes supported", m.imapSearchText))
	}
	return styleHelp.Render(fmt.Sprintf("  search (all folders): %s%s  · enter search · esc cancel · e.g. newsletter  from:simon  subject:invoice  to:team@", m.imapSearchText, cursor))
}
