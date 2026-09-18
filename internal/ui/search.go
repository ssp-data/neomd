package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sspaeti/neomd/internal/contacts"
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
// The query is expanded with addresses of known contacts whose display name
// matches (see expandSearchQueries) so searching "louise" also finds messages
// that only carry her bare address in the headers.
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
	queries := expandSearchQueries(query, m.contacts)
	return func() tea.Msg {
		var all []imap.Email
		seen := make(map[string]bool)
		var firstErr error
		for i, q := range queries {
			emails, err := cli.SearchAllFolders(nil, folders, q)
			if err != nil && i == 0 {
				firstErr = err // only the user's literal query reports errors
			}
			for _, e := range emails {
				key := fmt.Sprintf("%s\x00%d", e.Folder, e.UID)
				if !seen[key] {
					seen[key] = true
					all = append(all, e)
				}
			}
		}
		return imapSearchResultMsg{emails: all, query: query, err: firstErr}
	}
}

// expandSearchQueries returns the original query plus per-address queries for
// known contacts whose display name matches it. IMAP SEARCH can only match
// header text, and messages neomd sent carry bare addresses — without this a
// name search finds nothing in Sent. subject: queries are never expanded.
func expandSearchQueries(query string, cs *contacts.Store) []string {
	queries := []string{query}
	q := strings.TrimSpace(query)
	prefix := ""
	switch lower := strings.ToLower(q); {
	case strings.HasPrefix(lower, "subject:"):
		return queries
	case strings.HasPrefix(lower, "from:"):
		prefix, q = "from:", strings.TrimSpace(q[5:])
	case strings.HasPrefix(lower, "to:"):
		prefix, q = "to:", strings.TrimSpace(q[3:])
	}
	for _, addr := range cs.AddrsMatchingName(q, 3) {
		queries = append(queries, prefix+addr)
	}
	return queries
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
	m.imapSearchText = ""
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
	m.emails = msg.emails
	m.markedUIDs = make(map[uint32]bool)
	m.filterActive = false
	m.filterText = ""
	m.status = fmt.Sprintf("Everything — %d most recent emails across all folders. esc to close.", len(msg.emails))
	return m, m.sortEmails()
}

// conversationResultMsg carries results from a conversation/thread fetch.
type conversationResultMsg struct {
	emails []imap.Email
	err    error
}

// fetchConversationCmd fetches all emails related to the given email's
// conversation across key folders (Inbox, Sent, Archive, etc.).
func (m Model) fetchConversationCmd(e *imap.Email) tea.Cmd {
	cli := m.imapCli()
	f := m.cfg.Folders
	// Search folders likely to contain conversation parts.
	folders := []string{f.Inbox, f.Sent, f.Archive, f.Waiting, f.Someday, f.Scheduled}
	if f.Work != "" {
		folders = append(folders, f.Work)
	}
	// Add current folder if not already included.
	cur := e.Folder
	found := false
	for _, fo := range folders {
		if fo == cur {
			found = true
			break
		}
	}
	if !found && cur != "" {
		folders = append(folders, cur)
	}

	// Normalize subject and collect participants.
	subject := normalizeSubject(e.Subject)
	participants := make(map[string]bool)
	for _, addr := range imap.SplitAddrs(e.From) {
		participants[addr] = true
	}
	for _, addr := range imap.SplitAddrs(e.To) {
		participants[addr] = true
	}
	for _, addr := range imap.SplitAddrs(e.CC) {
		participants[addr] = true
	}

	return func() tea.Msg {
		emails, err := cli.FetchConversation(nil, folders, subject, participants)
		return conversationResultMsg{emails: emails, err: err}
	}
}

// handleConversationResult displays the conversation/thread view.
func (m *Model) handleConversationResult(msg conversationResultMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	m.imapSearchResults = false
	if msg.err != nil {
		m.status = "Thread: " + msg.err.Error()
		m.isError = true
		return m, nil
	}
	if len(msg.emails) == 0 {
		m.status = "No related emails found."
		return m, nil
	}
	m.offTabFolder = "Thread"
	m.emails = msg.emails
	m.markedUIDs = make(map[uint32]bool)
	m.filterActive = false
	m.filterText = ""
	m.status = fmt.Sprintf("Thread — %d email(s) in conversation. esc to close.", len(msg.emails))
	return m, m.sortEmails()
}

// senderAddr returns the first bare address from an email's From header,
// or "" if the header is empty or unparseable.
func senderAddr(e *imap.Email) string {
	addrs := imap.SplitAddrs(e.From)
	if len(addrs) == 0 {
		return ""
	}
	return addrs[0]
}

// senderResultMsg carries results from a per-sender search across folders.
type senderResultMsg struct {
	addr   string
	emails []imap.Email
	err    error
}

// fetchSenderCmd searches all folders for every email from the given
// email's sender address.
func (m Model) fetchSenderCmd(e *imap.Email) tea.Cmd {
	addr := senderAddr(e)
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
		if addr == "" {
			return senderResultMsg{addr: addr}
		}
		emails, err := cli.SearchAllFolders(nil, folders, "from:"+addr)
		return senderResultMsg{addr: addr, emails: emails, err: err}
	}
}

// handleSenderResult displays the "Sender" view — every email from one address.
func (m *Model) handleSenderResult(msg senderResultMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.status = "Sender: " + msg.err.Error()
		m.isError = true
		return m, nil
	}
	if len(msg.emails) == 0 {
		m.status = fmt.Sprintf("No emails found from %q.", msg.addr)
		return m, nil
	}
	m.offTabFolder = "Sender"
	m.emails = msg.emails
	m.markedUIDs = make(map[uint32]bool)
	m.filterActive = false
	m.filterText = ""
	m.status = fmt.Sprintf("Sender — %d email(s) from %s across all folders. esc to close.", len(msg.emails), msg.addr)
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

// mergeResultMsg carries the members of an opened user-merged thread.
type mergeResultMsg struct {
	title  string
	emails []imap.Email
	err    error
}

// fetchMergeCmd fetches every stored member of the merge (plus direct
// replies) across the same folders the T conversation view searches.
// fallback (the members visible in the current list) is shown when the
// server returns nothing, so the view never opens empty.
func (m Model) fetchMergeCmd(title string, ids []string, fallback []imap.Email) tea.Cmd {
	cli := m.imapCli()
	f := m.cfg.Folders
	folders := []string{f.Inbox, f.Sent, f.Archive, f.Waiting, f.Someday, f.Scheduled}
	if f.Work != "" {
		folders = append(folders, f.Work)
	}
	cur := m.activeFolder()
	found := false
	for _, fo := range folders {
		if fo == cur {
			found = true
			break
		}
	}
	if !found && cur != "" {
		folders = append(folders, cur)
	}
	return func() tea.Msg {
		emails, err := cli.SearchByMessageIDs(nil, folders, ids)
		if err == nil && len(emails) == 0 {
			emails = fallback
		}
		return mergeResultMsg{title: title, emails: emails, err: err}
	}
}

// handleMergeResult displays the opened merge as an off-tab view.
func (m *Model) handleMergeResult(msg mergeResultMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	m.imapSearchResults = false
	if msg.err != nil {
		m.status = "Merge: " + msg.err.Error()
		m.isError = true
		return m, nil
	}
	m.offTabFolder = "Merged: " + msg.title
	m.emails = msg.emails
	m.markedUIDs = make(map[uint32]bool)
	m.filterActive = false
	m.filterText = ""
	m.status = fmt.Sprintf("Merged %q — %d email(s). esc to close · :unmerge removes the cursor email.", msg.title, len(msg.emails))
	return m, m.sortEmails()
}
