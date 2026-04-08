// Package ui contains the bubbletea TUI model for neomd.
package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/editor"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/render"
	"github.com/sspaeti/neomd/internal/screener"
	"github.com/sspaeti/neomd/internal/smtp"
)

// viewState is the current screen.
type viewState int

const (
	stateInbox   viewState = iota
	stateReading           // reading a single email
	stateCompose           // composing a new email
	statePresend           // pre-send review: add attachments, then send or edit again
	stateHelp              // help overlay
	stateWelcome           // first-run welcome popup
)

// async message types
type (
	emailsLoadedMsg struct {
		emails []imap.Email
		folder string
	}
	bodyLoadedMsg struct {
		email       *imap.Email
		body        string
		rawHTML     string // original HTML part, empty for plain-text emails
		webURL      string // canonical "view online" URL (List-Post header or plain-text preamble)
		attachments []imap.Attachment
	}
	sendDoneMsg struct {
		err           error
		warning       string
		replyToUID    uint32 // set \Answered on this email after send
		replyToFolder string
	}
	screenDoneMsg     struct{ err error }
	autoScreenDoneMsg struct {
		moved int
		err   error
	}
	deepScreenReadyMsg struct {
		moves []autoScreenMove
		total int
	}
	// deepScreenCountMsg is returned by phase-1: UID SEARCH finished, total known.
	deepScreenCountMsg struct {
		uids  []uint32
		total int
	}
	// deepScreenBatchMsg carries accumulated results between batches.
	deepScreenBatchMsg struct {
		emails    []imap.Email // accumulated so far
		remaining []uint32     // UIDs not yet fetched
		total     int
	}
	// resetToScreenReadyMsg is returned once we know how many emails are in ToScreen.
	resetToScreenReadyMsg struct{ uids []uint32 }
	// folderCountsMsg carries unseen counts for watched folder tabs.
	folderCountsMsg struct{ counts map[string]int }
	// deleteAllReadyMsg carries UIDs to permanently delete after y/n confirm.
	deleteAllReadyMsg struct {
		uids   []uint32
		folder string
	}
	// ensureFoldersDoneMsg reports which folders were created.
	ensureFoldersDoneMsg struct {
		created []string
		err     error
	}
	moveDoneMsg struct {
		err  error
		undo []undoMove
	}
	batchDoneMsg struct {
		err  error
		undo []undoMove
	}
	undoDoneMsg       struct{}
	toggleSeenDoneMsg struct {
		uid  uint32
		seen bool
		err  error
	}
	errMsg struct{ err error }
	// background sync (runs every bgSyncInterval while neomd is open)
	bgSyncTickMsg     struct{}
	bgInboxFetchedMsg struct{ emails []imap.Email }
	bgScreenDoneMsg   struct{ moved, total int }
	// attachPickDoneMsg carries paths selected via the file picker (yazi etc.)
	attachPickDoneMsg struct{ paths []string }
	// bulkProgressMsg is sent during long-running batch operations to update the status bar.
	bulkProgressMsg struct {
		moved, total int
		label        string
	}
	saveDraftDoneMsg  struct{ err error }
	attachOpenDoneMsg struct {
		path string
		err  error
	}
	editorDoneMsg struct {
		to, cc, bcc, subject, body string
		err                        error
		aborted                    bool // true when file was unchanged (ZQ / :q!)
	}
)

// bulkOp tracks progress of long-running batch operations.
// Shared by pointer between the model (reader) and goroutines (writer).
type bulkOp struct {
	moved atomic.Int64
	total int64
	label string // "Screening", "Moving", etc.
}

const maxUndoStack = 20

func (m Model) newBulkOp(label string, total int) *bulkOp {
	if total <= m.cfg.UI.BulkThreshold() {
		return nil // small batches don't need progress tracking
	}
	return &bulkOp{label: label, total: int64(total)}
}

func (b *bulkOp) String() string {
	if b == nil {
		return ""
	}
	return fmt.Sprintf("%s: %d/%d…", b.label, b.moved.Load(), b.total)
}

// startBulk initializes a bulk progress tracker. Call before launching batch commands.
func (m *Model) startBulk(label string, total int) {
	m.bulkProgress = m.newBulkOp(label, total)
}

// Version is set by main.go at startup (from build-time ldflags).
var Version = "dev"

// neomdTempDir returns /tmp/neomd/, creating it if needed.
// Using a dedicated subdirectory keeps temp files discoverable (e.g. recovering
// a draft after a crash) and avoids cluttering /tmp/.
func neomdTempDir() string {
	dir := filepath.Join(os.TempDir(), "neomd")
	os.MkdirAll(dir, 0700) //nolint
	return dir
}

// backupFile holds a backup's full path and modification time.
type backupFile struct {
	path    string
	modTime time.Time
}

// listBackupsByAge returns files in dir sorted oldest-first.
func listBackupsByAge(dir string) []backupFile {
	entries, _ := os.ReadDir(dir)
	files := make([]backupFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, backupFile{
			path:    filepath.Join(dir, e.Name()),
			modTime: info.ModTime(),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	return files
}

// backupDraft copies a compose temp file to ~/.cache/neomd/drafts/ before it is
// deleted. Keeps at most maxBackups files, pruning the oldest.
func backupDraft(tmpPath string, maxBackups int) {
	if maxBackups <= 0 {
		return // disabled
	}
	dir := config.DraftsBackupDir()
	dst := filepath.Join(dir, filepath.Base(tmpPath))
	src, err := os.ReadFile(tmpPath)
	if err != nil || len(src) == 0 {
		return
	}
	_ = os.WriteFile(dst, src, 0600)

	// Prune oldest if over limit.
	files := listBackupsByAge(dir)
	for i := 0; i < len(files)-maxBackups; i++ {
		_ = os.Remove(files[i].path)
	}
}

// maskEmail masks the local part of an email address: "user@example.com" → "u***@example.com".
// For "Name <email>" format, masks the email part only.
func maskEmail(s string) string {
	// Extract email from "Name <email>" format
	email := s
	prefix := ""
	if i := strings.LastIndex(s, "<"); i >= 0 {
		if j := strings.Index(s[i:], ">"); j >= 0 {
			prefix = s[:i]
			email = s[i+1 : i+j]
		}
	}
	// Mask local part
	at := strings.Index(email, "@")
	if at <= 0 {
		return s // not an email, return as-is
	}
	local := email[:at]
	domain := email[at:]
	masked := string(local[0]) + "***" + domain
	if prefix != "" {
		return prefix + "<" + masked + ">"
	}
	return masked
}

// writeDebugReport generates a diagnostic report and opens it in the reader.
func (m Model) writeDebugReport() tea.Cmd {
	return func() tea.Msg {
		var b strings.Builder
		b.WriteString("# neomd debug report\n\n")
		b.WriteString(fmt.Sprintf("Version: %s\n", Version))
		b.WriteString(fmt.Sprintf("Time: %s\n", time.Now().Format(time.RFC3339)))
		b.WriteString(fmt.Sprintf("Config: %s\n\n", config.DefaultPath()))

		// Accounts
		b.WriteString("## Accounts\n\n")
		for i, a := range m.accounts {
			active := ""
			if i == m.accountI {
				active = " (active)"
			}
			b.WriteString(fmt.Sprintf("- **%s**%s\n", a.Name, active))
			b.WriteString(fmt.Sprintf("  - IMAP: `%s`\n", a.IMAP))
			b.WriteString(fmt.Sprintf("  - SMTP: `%s`\n", a.SMTP))
			b.WriteString(fmt.Sprintf("  - User: `%s`\n", maskEmail(a.User)))
			b.WriteString(fmt.Sprintf("  - From: `%s`\n", maskEmail(a.From)))
			hasPass := "set"
			if a.Password == "" {
				hasPass = "EMPTY"
			}
			b.WriteString(fmt.Sprintf("  - Password: %s\n", hasPass))
		}

		// Connection test
		b.WriteString("\n## IMAP Connection\n\n")
		for i, cli := range m.clients {
			name := "unknown"
			if i < len(m.accounts) {
				name = m.accounts[i].Name
			}
			b.WriteString(fmt.Sprintf("- **%s** → `%s`\n", name, cli.Addr()))
			if err := cli.Ping(nil); err != nil {
				b.WriteString(fmt.Sprintf("  - PING: FAILED — `%s`\n", err))
			} else {
				b.WriteString("  - PING: OK\n")
			}
		}

		// Folders
		b.WriteString("\n## Folder Mapping\n\n")
		f := m.cfg.Folders
		folders := [][2]string{
			{"Inbox", f.Inbox}, {"Sent", f.Sent}, {"Trash", f.Trash},
			{"Drafts", f.Drafts}, {"ToScreen", f.ToScreen}, {"Feed", f.Feed},
			{"PaperTrail", f.PaperTrail}, {"ScreenedOut", f.ScreenedOut},
			{"Archive", f.Archive}, {"Waiting", f.Waiting},
			{"Scheduled", f.Scheduled}, {"Someday", f.Someday}, {"Spam", f.Spam},
			{"Work", f.Work},
		}
		for _, kv := range folders {
			val := kv[1]
			if val == "" {
				val = "(not set)"
			}
			b.WriteString(fmt.Sprintf("- %s → `%s`\n", kv[0], val))
		}

		// Tab order
		b.WriteString(fmt.Sprintf("\nTab order: %s\n", strings.Join(m.folders, " → ")))
		b.WriteString(fmt.Sprintf("Active tab: %s (index %d)\n", m.folders[m.activeFolderI], m.activeFolderI))

		// Screener lists
		b.WriteString("\n## Screener Lists\n\n")
		sc := m.cfg.Screener
		lists := [][2]string{
			{"screened_in", sc.ScreenedIn}, {"screened_out", sc.ScreenedOut},
			{"feed", sc.Feed}, {"papertrail", sc.PaperTrail}, {"spam", sc.Spam},
		}
		for _, kv := range lists {
			path := kv[1]
			if path == "" {
				b.WriteString(fmt.Sprintf("- %s: (not set)\n", kv[0]))
				continue
			}
			info, err := os.Stat(path)
			if err != nil {
				b.WriteString(fmt.Sprintf("- %s: `%s` — MISSING (%s)\n", kv[0], path, err))
			} else {
				b.WriteString(fmt.Sprintf("- %s: `%s` (%d bytes)\n", kv[0], path, info.Size()))
			}
		}

		// UI config
		b.WriteString("\n## UI Config\n\n")
		b.WriteString(fmt.Sprintf("- inbox_count: %d\n", m.cfg.UI.InboxCount))
		b.WriteString(fmt.Sprintf("- theme: %s\n", m.cfg.UI.Theme))
		b.WriteString(fmt.Sprintf("- auto_screen_on_load: %v\n", m.cfg.UI.AutoScreen()))
		b.WriteString(fmt.Sprintf("- bg_sync_interval: %d min\n", m.cfg.UI.BgSyncInterval))

		// Current state
		b.WriteString("\n## Current State\n\n")
		b.WriteString(fmt.Sprintf("- Loaded emails: %d\n", len(m.emails)))
		b.WriteString(fmt.Sprintf("- Loading: %v\n", m.loading))
		b.WriteString(fmt.Sprintf("- View state: %d\n", m.state))
		b.WriteString(fmt.Sprintf("- Last status: %s\n", m.status))
		b.WriteString(fmt.Sprintf("- Is error: %v\n", m.isError))

		// Folder unseen counts
		b.WriteString("\n## Unseen Counts\n\n")
		if len(m.folderCounts) == 0 {
			b.WriteString("(none loaded yet)\n")
		}
		for folder, n := range m.folderCounts {
			b.WriteString(fmt.Sprintf("- %s: %d\n", folder, n))
		}

		// Write to file
		path := filepath.Join(neomdTempDir(), "debug.log")
		if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
			return errMsg{fmt.Errorf("write debug report: %w", err)}
		}

		// Return as body to display in reader
		return bodyLoadedMsg{
			email: &imap.Email{Subject: "neomd debug report", From: "neomd", Folder: "debug"},
			body:  b.String(),
		}
	}
}

// pendingSendData holds a composed message waiting in the pre-send review screen.
type pendingSendData struct {
	to, cc, bcc, subject, body string
	// replyToUID/replyToFolder track the original email when this is a reply,
	// so we can set \Answered after sending. Zero means not a reply.
	replyToUID    uint32
	replyToFolder string
}

// undoMove records one IMAP move so it can be reversed with u.
type undoMove struct {
	uid        uint32
	fromFolder string
	toFolder   string
}

// autoScreenMove is a planned (not yet executed) IMAP move.
type autoScreenMove struct {
	email *imap.Email
	dst   string
}

// Model is the root bubbletea model.
type Model struct {
	cfg      *config.Config
	accounts []config.AccountConfig // all configured accounts
	clients  []*imap.Client         // one IMAP client per account
	accountI int                    // index of the active account
	screener *screener.Screener

	state   viewState
	width   int
	height  int
	loading bool

	// Bulk operation progress — shared pointer, written by goroutines, read by view.
	bulkProgress *bulkOp

	// Folder switcher
	folders       []string
	activeFolderI int
	offTabFolder  string // non-empty when viewing a folder not in the tab bar (e.g. "Spam", "Drafts")

	// Inbox
	inbox   list.Model
	emails  []imap.Email
	spinner spinner.Model

	// Reader
	reader          viewport.Model
	openEmail       *imap.Email
	openBody        string            // markdown body used by the TUI reader
	openHTMLBody    string            // original HTML part; used by openInExternalViewer when available
	openWebURL      string            // canonical "view online" URL for ctrl+o (may be empty)
	openAttachments []imap.Attachment // attachments of the currently open email
	openLinks       []emailLink       // extracted links from the email body
	readerPending   string            // chord prefix in reader (space for link open)

	// Compose / pre-send
	compose      composeModel
	attachments  []string // files to attach to the next send (cleared after send)
	pendingSend  *pendingSendData
	presendFromI int // index into presendFroms() for the From field cycle

	// Status / error
	status  string
	isError bool

	// Auto-screen dry-run: populated by S, cleared by y/n
	pendingMoves []autoScreenMove

	// Marked emails for batch operations (UID → true)
	markedUIDs map[uint32]bool

	// Undo stack: each entry is a batch of moves that can be reversed with u.
	// Screener operations (I/O/F/P/$) are not undoable — they also modify .txt files.
	undoStack [][]undoMove

	// Forward/Reply: when true, bodyLoadedMsg launches the action instead of reader
	pendingForward  bool
	pendingReply    bool
	pendingReplyAll bool

	// Chord prefix: "g" or "M" while waiting for second key
	pendingKey string

	// prevState is the state to return to when closing the help overlay
	prevState viewState

	// helpSearch is the live filter string typed in the help overlay
	helpSearch string

	// cmdMode / cmdText / cmdTabI implement vim-style ":" command line.
	cmdMode    bool
	cmdText    string
	cmdTabI    int      // cycle index for tab-completion
	cmdHistory []string // up to 5 most-recent distinct commands (newest first)
	cmdHistI   int      // -1 = not browsing history; 0..n = history index

	// IMAP server-side search (ctrl+/)
	imapSearchActive  bool   // true while typing in the IMAP search prompt
	imapSearchText    string // current IMAP search query
	imapSearchResults bool   // true when displaying IMAP search results (esc to clear)

	// filterActive / filterText implement our own inbox search.
	// We bypass bubbles/list's built-in filter because SetShowTitle(false)
	// hides the filter input. filterActive is true while the user is typing.
	filterActive bool
	filterText   string

	// pendingResetUIDs holds ToScreen UIDs awaiting y/n confirmation before
	// being bulk-moved back to Inbox.
	pendingResetUIDs []uint32

	// pendingDeleteAll holds UIDs + folder awaiting y/n before permanent deletion.
	pendingDeleteAll *deleteAllReadyMsg

	// folderCounts holds unseen message counts for watched folder tabs.
	// Keys are tab labels: "Inbox", "PaperTrail", "Waiting", "Scheduled".
	folderCounts map[string]int

	// Sort state. sortField is one of "date", "from", "subject", "size".
	// sortReverse=true means newest/largest/Z-first (descending).
	// Default: date descending (newest first).
	sortField   string
	sortReverse bool
}

// New creates and initialises the TUI model.
func New(cfg *config.Config, clients []*imap.Client, sc *screener.Screener) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	compose := newComposeModel()
	compose.knownAddrs = sc.AllAddresses()

	return Model{
		cfg:        cfg,
		accounts:   cfg.ActiveAccounts(),
		clients:    clients,
		screener:   sc,
		state:      stateInbox,
		loading:    true,
		folders:    cfg.Folders.TabLabels(),
		cmdHistory: loadCmdHistory(config.HistoryPath()),
		cmdHistI:   -1,
		// Note: Spam is intentionally excluded from tabs — use :go-spam to visit.
		compose:     compose,
		spinner:     sp,
		markedUIDs:  make(map[uint32]bool),
		sortField:   "date",
		sortReverse: true, // newest first
	}
}

// tokenSourceFor returns the OAuth2 token source for the account with the
// given name, or nil if the account uses plain password authentication.
func (m Model) tokenSourceFor(accountName string) func() (string, error) {
	for i, acc := range m.accounts {
		if acc.Name == accountName && i < len(m.clients) {
			return m.clients[i].TokenSource()
		}
	}
	return nil
}

// activeAccount returns the currently selected AccountConfig.
func (m Model) activeAccount() config.AccountConfig {
	if m.accountI < len(m.accounts) {
		return m.accounts[m.accountI]
	}
	return m.accounts[0]
}

// presendFroms returns all available From addresses: all accounts first (in
// config order), then any [[senders]] aliases. This lets the user cycle to any
// account's From address regardless of which account is currently active.
func (m Model) presendFroms() []string {
	froms := make([]string, 0, len(m.accounts)+len(m.cfg.Senders))
	for _, a := range m.accounts {
		froms = append(froms, a.From)
	}
	for _, s := range m.cfg.Senders {
		froms = append(froms, s.From)
	}
	return froms
}

// presendFrom returns the currently selected From address.
func (m Model) presendFrom() string {
	froms := m.presendFroms()
	if m.presendFromI < len(froms) {
		return froms[m.presendFromI]
	}
	return froms[0]
}

// presendSMTPAccount returns the AccountConfig whose SMTP credentials to use
// for the currently selected From address.
//   - If presendFromI points to an account, that account's SMTP is used directly.
//   - If it points to a [[senders]] entry with an account name, that account's SMTP is used.
//   - Otherwise falls back to the active account.
func (m Model) presendSMTPAccount() config.AccountConfig {
	if m.presendFromI < len(m.accounts) {
		return m.accounts[m.presendFromI]
	}
	senderIdx := m.presendFromI - len(m.accounts)
	if senderIdx < len(m.cfg.Senders) {
		s := m.cfg.Senders[senderIdx]
		if s.Account != "" {
			for _, a := range m.accounts {
				if strings.EqualFold(a.Name, s.Account) {
					return a
				}
			}
		}
	}
	return m.activeAccount()
}

// imapCli returns the IMAP client for the active account.
func (m Model) imapCli() *imap.Client {
	if m.accountI < len(m.clients) {
		return m.clients[m.accountI]
	}
	return m.clients[0]
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.spinner.Tick,
		m.fetchFolderCmd(m.activeFolder()),
		m.scheduleBgSync(),
	}
	if config.IsFirstRun() {
		cmds = append(cmds, m.ensureFoldersCmd())
	}
	return tea.Batch(cmds...)
}

// activeFolder maps the active tab label to an IMAP mailbox name.
func (m Model) activeFolder() string {
	switch m.folders[m.activeFolderI] {
	case "ToScreen":
		return m.cfg.Folders.ToScreen
	case "Feed":
		return m.cfg.Folders.Feed
	case "PaperTrail":
		return m.cfg.Folders.PaperTrail
	case "Sent":
		return m.cfg.Folders.Sent
	case "Trash":
		return m.cfg.Folders.Trash
	case "Archive":
		return m.cfg.Folders.Archive
	case "Waiting":
		return m.cfg.Folders.Waiting
	case "Scheduled":
		return m.cfg.Folders.Scheduled
	case "Someday":
		return m.cfg.Folders.Someday
	case "ScreenedOut":
		return m.cfg.Folders.ScreenedOut
	case "Spam":
		return m.cfg.Folders.Spam
	case "Work":
		return m.cfg.Folders.Work
	default:
		return m.cfg.Folders.Inbox
	}
}

// ── Commands ─────────────────────────────────────────────────────────────

func (m Model) fetchFolderCmd(folder string) tea.Cmd {
	return func() tea.Msg {
		emails, err := m.imapCli().FetchHeaders(nil, folder, m.cfg.UI.InboxCount)
		if err != nil {
			return errMsg{err}
		}
		return emailsLoadedMsg{emails: emails, folder: folder}
	}
}

func (m Model) fetchBodyCmd(e *imap.Email) tea.Cmd {
	return func() tea.Msg {
		body, rawHTML, webURL, attachments, err := m.imapCli().FetchBody(nil, e.Folder, e.UID)
		if err != nil {
			return errMsg{err}
		}
		return bodyLoadedMsg{email: e, body: body, rawHTML: rawHTML, webURL: webURL, attachments: attachments}
	}
}

func (m Model) sendEmailCmd(smtpAcct config.AccountConfig, from, to, cc, bcc, subject, body string, attachments []string, replyToUID uint32, replyToFolder string) tea.Cmd {
	h, p := splitAddr(smtpAcct.SMTP)
	cfg := smtp.Config{
		Host:        h,
		Port:        p,
		User:        smtpAcct.User,
		Password:    smtpAcct.Password,
		From:        from,
		TokenSource: m.tokenSourceFor(smtpAcct.Name),
	}
	cli := m.imapCli()
	sentFolder := m.cfg.Folders.Sent
	return func() tea.Msg {
		// Build raw MIME once — reused for both SMTP delivery and Sent copy.
		// BCC is intentionally excluded from headers but included in RCPT TO.
		raw, err := smtp.BuildMessage(from, to, cc, subject, body, attachments)
		if err != nil {
			return sendDoneMsg{err: fmt.Errorf("build message: %w", err)}
		}
		toAddrs := collectRcptTo(to, cc, bcc)
		if err := smtp.SendRaw(cfg, toAddrs, raw); err != nil {
			return sendDoneMsg{err: err}
		}
		// Save copy to Sent; non-fatal if it fails, but warn user.
		if saveErr := cli.SaveSent(nil, sentFolder, raw); saveErr != nil {
			return sendDoneMsg{warning: "Sent, but failed to save to Sent folder: " + saveErr.Error(), replyToUID: replyToUID, replyToFolder: replyToFolder}
		}
		// Mark original email as \Answered (non-fatal).
		if replyToUID > 0 && replyToFolder != "" {
			_ = cli.MarkAnswered(nil, replyToFolder, replyToUID)
		}
		return sendDoneMsg{replyToUID: replyToUID, replyToFolder: replyToFolder}
	}
}

// collectRcptTo returns deduplicated bare email addresses for SMTP RCPT TO.
func collectRcptTo(to, cc, bcc string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, field := range []string{to, cc, bcc} {
		for _, addr := range strings.Split(field, ",") {
			if a := extractEmailAddr(strings.TrimSpace(addr)); a != "" && !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	return out
}

// toggleSeenCmd flips the \Seen flag on an email and updates local state.
func (m Model) toggleSeenCmd(e *imap.Email) tea.Cmd {
	uid := e.UID
	folder := e.Folder
	newSeen := !e.Seen
	return func() tea.Msg {
		var err error
		if newSeen {
			err = m.imapCli().MarkSeen(nil, folder, uid)
		} else {
			err = m.imapCli().MarkUnseen(nil, folder, uid)
		}
		return toggleSeenDoneMsg{uid: uid, seen: newSeen, err: err}
	}
}

// moveEmailCmd moves a single email to dst without updating screener lists.
func (m Model) moveEmailCmd(e *imap.Email, dst string) tea.Cmd {
	src := e.Folder
	uid := e.UID
	return func() tea.Msg {
		destUID, err := m.imapCli().MoveMessage(nil, src, uid, dst)
		return moveDoneMsg{err: err, undo: []undoMove{{uid: destUID, fromFolder: src, toFolder: dst}}}
	}
}

// targetEmails returns marked emails if any are marked, otherwise just the cursor email.
func (m Model) targetEmails() []imap.Email {
	if len(m.markedUIDs) > 0 {
		var out []imap.Email
		for _, e := range m.emails {
			if m.markedUIDs[e.UID] {
				out = append(out, e)
			}
		}
		return out
	}
	if e := selectedEmail(m.inbox); e != nil {
		return []imap.Email{*e}
	}
	return nil
}

// batchMoveCmd moves a slice of emails to dst, emitting batchDoneMsg.
func (m Model) batchMoveCmd(emails []imap.Email, dst string) tea.Cmd {
	type mv struct {
		folder string
		uid    uint32
	}
	moves := make([]mv, len(emails))
	for i, e := range emails {
		moves[i] = mv{e.Folder, e.UID}
	}
	bp := m.bulkProgress
	return func() tea.Msg {
		undos := make([]undoMove, 0, len(moves))
		for i, mv := range moves {
			destUID, err := m.imapCli().MoveMessage(nil, mv.folder, mv.uid, dst)
			if err != nil {
				return batchDoneMsg{err: fmt.Errorf("stopped after %d/%d: %w", i, len(moves), err), undo: undos}
			}
			undos = append(undos, undoMove{uid: destUID, fromFolder: mv.folder, toFolder: dst})
			if bp != nil {
				bp.moved.Add(1)
			}
		}
		return batchDoneMsg{undo: undos}
	}
}

// undoMovesCmd reverses a batch of moves by moving each email back to its
// original folder. Non-fatal per-email errors are reported as a batchDoneMsg.
func (m Model) undoMovesCmd(moves []undoMove) tea.Cmd {
	cli := m.imapCli()
	return func() tea.Msg {
		for i, u := range moves {
			if _, err := cli.MoveMessage(nil, u.toFolder, u.uid, u.fromFolder); err != nil {
				return batchDoneMsg{err: fmt.Errorf("undo stopped after %d/%d: %w", i, len(moves), err)}
			}
		}
		return undoDoneMsg{}
	}
}

// batchScreenerCmd runs a screener action (I/O/F/P) on multiple emails.
func (m Model) batchScreenerCmd(emails []imap.Email, action string) tea.Cmd {
	sc := m.screener
	cfg := m.cfg
	type op struct {
		from, srcFolder string
		uid             uint32
		dst             string
	}
	ops := make([]op, 0, len(emails))
	for _, e := range emails {
		var dst string
		switch action {
		case "I":
			dst = cfg.Folders.Inbox
		case "O":
			dst = cfg.Folders.ScreenedOut
		case "F":
			dst = cfg.Folders.Feed
		case "P":
			dst = cfg.Folders.PaperTrail
		case "$":
			dst = cfg.Folders.Spam
		}
		ops = append(ops, op{e.From, e.Folder, e.UID, dst})
	}
	bp := m.bulkProgress
	return func() tea.Msg {
		for i, o := range ops {
			// Move first, classify after — if move fails, screener file stays unchanged.
			if o.dst != "" && o.dst != o.srcFolder {
				if _, err := m.imapCli().MoveMessage(nil, o.srcFolder, o.uid, o.dst); err != nil {
					return batchDoneMsg{err: fmt.Errorf("stopped after %d/%d: %w", i, len(ops), err)}
				}
			}
			var err error
			switch action {
			case "I":
				err = sc.Approve(o.from)
			case "O":
				err = sc.Block(o.from)
			case "F":
				err = sc.MarkFeed(o.from)
			case "P":
				err = sc.MarkPaperTrail(o.from)
			case "$":
				err = sc.MarkSpam(o.from)
			}
			if err != nil {
				return batchDoneMsg{err: fmt.Errorf("stopped after %d/%d: %w", i, len(ops), err)}
			}
			if bp != nil {
				bp.moved.Add(1)
			}
		}
		return batchDoneMsg{}
	}
}

// markAllSeenCmd marks every currently loaded email in the folder as \Seen.
func (m Model) markAllSeenCmd() tea.Cmd {
	type op struct {
		folder string
		uid    uint32
	}
	var ops []op
	for _, e := range m.emails {
		if !e.Seen {
			ops = append(ops, op{e.Folder, e.UID})
		}
	}
	if len(ops) == 0 {
		return nil
	}
	return func() tea.Msg {
		for _, o := range ops {
			if err := m.imapCli().MarkSeen(nil, o.folder, o.uid); err != nil {
				return batchDoneMsg{err: err}
			}
		}
		return batchDoneMsg{}
	}
}

// batchToggleSeenCmd toggles \Seen on multiple emails, emitting batchDoneMsg.
func (m Model) batchToggleSeenCmd(emails []imap.Email) tea.Cmd {
	type op struct {
		folder   string
		uid      uint32
		markSeen bool
	}
	ops := make([]op, len(emails))
	for i, e := range emails {
		ops[i] = op{e.Folder, e.UID, !e.Seen}
	}
	return func() tea.Msg {
		for _, o := range ops {
			var err error
			if o.markSeen {
				err = m.imapCli().MarkSeen(nil, o.folder, o.uid)
			} else {
				err = m.imapCli().MarkUnseen(nil, o.folder, o.uid)
			}
			if err != nil {
				return batchDoneMsg{err: err}
			}
		}
		return batchDoneMsg{}
	}
}

// classifyForScreen classifies a slice of inbox emails in-memory (O(1) map
// lookups) and returns planned moves. emails must live at least as long as the
// returned moves (pointers into the slice are stored).
func (m Model) classifyForScreen(emails []imap.Email) []autoScreenMove {
	inboxFolder := m.cfg.Folders.Inbox
	var moves []autoScreenMove
	for i := range emails {
		e := &emails[i]
		cat := m.screener.Classify(e.From)
		var dst string
		switch cat {
		case screener.CategorySpam:
			dst = m.cfg.Folders.Spam
		case screener.CategoryScreenedOut:
			dst = m.cfg.Folders.ScreenedOut
		case screener.CategoryFeed:
			dst = m.cfg.Folders.Feed
		case screener.CategoryPaperTrail:
			dst = m.cfg.Folders.PaperTrail
		case screener.CategoryToScreen:
			dst = m.cfg.Folders.ToScreen
		}
		if dst != "" && dst != inboxFolder {
			moves = append(moves, autoScreenMove{email: e, dst: dst})
		}
	}
	return moves
}

// previewAutoScreen classifies the currently loaded inbox emails (no IMAP).
func (m Model) previewAutoScreen() []autoScreenMove {
	return m.classifyForScreen(m.emails)
}

// deepScreenCmd is phase 1: just UID SEARCH — fast regardless of mailbox size.
// Returns deepScreenCountMsg so the UI can show the total before phase 2 starts.
func (m Model) deepScreenCmd() tea.Cmd {
	inboxFolder := m.cfg.Folders.Inbox
	return func() tea.Msg {
		uids, err := m.imapCli().SearchUIDs(nil, inboxFolder)
		if err != nil {
			return errMsg{err}
		}
		return deepScreenCountMsg{uids: uids, total: len(uids)}
	}
}

// deepScreenClassifyCmd is phase 2: fetch ONE batch of UIDs (1000 at a time)
// and return deepScreenBatchMsg so the UI can show per-batch progress.
// accumulated holds headers already fetched in prior batches.
func (m Model) deepScreenClassifyCmd(accumulated []imap.Email, remaining []uint32, total int) tea.Cmd {
	inboxFolder := m.cfg.Folders.Inbox
	const batchSize = 1000
	return func() tea.Msg {
		end := batchSize
		if end > len(remaining) {
			end = len(remaining)
		}
		batch, err := m.imapCli().FetchHeadersByUID(nil, inboxFolder, remaining[:end])
		if err != nil {
			return errMsg{err}
		}
		return deepScreenBatchMsg{
			emails:    append(accumulated, batch...),
			remaining: remaining[end:],
			total:     total,
		}
	}
}

// resetToScreenSearchCmd is phase 1: just count UIDs in ToScreen so we can
// show the user a confirmation before moving anything.
func (m Model) resetToScreenSearchCmd() tea.Cmd {
	folder := m.cfg.Folders.ToScreen
	return func() tea.Msg {
		uids, err := m.imapCli().SearchUIDs(nil, folder)
		if err != nil {
			return errMsg{err}
		}
		return resetToScreenReadyMsg{uids: uids}
	}
}

// resetToScreenMoveCmd bulk-moves all given UIDs from ToScreen back to Inbox.
func (m Model) resetToScreenMoveCmd(uids []uint32) tea.Cmd {
	src := m.cfg.Folders.ToScreen
	dst := m.cfg.Folders.Inbox
	return func() tea.Msg {
		for i, uid := range uids {
			if _, err := m.imapCli().MoveMessage(nil, src, uid, dst); err != nil {
				return batchDoneMsg{err: fmt.Errorf("stopped after %d/%d: %w", i, len(uids), err)}
			}
		}
		return batchDoneMsg{}
	}
}

// ensureFoldersCmd creates any configured folders that don't exist yet.
func (m Model) ensureFoldersCmd() tea.Cmd {
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
		created, err := m.imapCli().EnsureFolders(nil, folders)
		return ensureFoldersDoneMsg{created: created, err: err}
	}
}

// deleteAllSearchCmd is phase 1: count UIDs in the current folder before
// asking for confirmation.
func (m Model) deleteAllSearchCmd() tea.Cmd {
	folder := m.activeFolder()
	return func() tea.Msg {
		uids, err := m.imapCli().SearchUIDs(nil, folder)
		if err != nil {
			return errMsg{err}
		}
		return deleteAllReadyMsg{uids: uids, folder: folder}
	}
}

// emptyTrashSearchCmd is like deleteAllSearchCmd but always targets Trash.
func (m Model) emptyTrashSearchCmd() tea.Cmd {
	folder := m.cfg.Folders.Trash
	return func() tea.Msg {
		uids, err := m.imapCli().SearchUIDs(nil, folder)
		if err != nil {
			return errMsg{err}
		}
		return deleteAllReadyMsg{uids: uids, folder: folder}
	}
}

// deleteAllExecCmd permanently deletes all given UIDs from folder.
func (m Model) deleteAllExecCmd(folder string, uids []uint32) tea.Cmd {
	return func() tea.Msg {
		return batchDoneMsg{err: m.imapCli().ExpungeAll(nil, folder, uids)}
	}
}

// fetchFolderCountsCmd fetches unseen counts for the four watched tabs in the
// background using IMAP STATUS (no SELECT, very fast).
func (m Model) fetchFolderCountsCmd() tea.Cmd {
	folders := map[string]string{
		"Inbox":      m.cfg.Folders.Inbox,
		"PaperTrail": m.cfg.Folders.PaperTrail,
		"Waiting":    m.cfg.Folders.Waiting,
		"Scheduled":  m.cfg.Folders.Scheduled,
	}
	return func() tea.Msg {
		counts, _ := m.imapCli().FetchUnseenCounts(nil, folders)
		return folderCountsMsg{counts: counts}
	}
}

// scheduleBgSync returns a Cmd that fires bgSyncTickMsg after the configured
// interval. Returns nil (no-op) when bg_sync_interval = 0 (disabled).
func (m Model) scheduleBgSync() tea.Cmd {
	mins := m.cfg.UI.BgSyncInterval
	if mins <= 0 {
		return nil
	}
	return tea.Tick(time.Duration(mins)*time.Minute, func(time.Time) tea.Msg { return bgSyncTickMsg{} })
}

// bgFetchInboxCmd silently fetches inbox headers for background screening.
// Errors are swallowed — a transient network hiccup shouldn't disrupt the UI.
func (m Model) bgFetchInboxCmd() tea.Cmd {
	return func() tea.Msg {
		emails, err := m.imapCli().FetchHeaders(nil, m.cfg.Folders.Inbox, m.cfg.UI.InboxCount)
		if err != nil {
			return bgSyncTickMsg{} // reschedule retry on next tick instead of errMsg
		}
		return bgInboxFetchedMsg{emails: emails}
	}
}

// bgExecAutoScreenCmd silently moves emails and returns bgScreenDoneMsg.
func (m Model) bgExecAutoScreenCmd(moves []autoScreenMove) tea.Cmd {
	src := m.cfg.Folders.Inbox
	total := len(moves)
	return func() tea.Msg {
		moved := 0
		for _, mv := range moves {
			if _, err := m.imapCli().MoveMessage(nil, src, mv.email.UID, mv.dst); err != nil {
				break
			}
			moved++
		}
		return bgScreenDoneMsg{moved: moved, total: total}
	}
}

// execAutoScreenCmd performs the IMAP moves for a pre-approved list of moves.
func (m Model) execAutoScreenCmd(moves []autoScreenMove) tea.Cmd {
	src := m.cfg.Folders.Inbox
	bp := m.bulkProgress
	return func() tea.Msg {
		for i, mv := range moves {
			if _, err := m.imapCli().MoveMessage(nil, src, mv.email.UID, mv.dst); err != nil {
				return autoScreenDoneMsg{moved: i, err: err}
			}
			if bp != nil {
				bp.moved.Add(1)
			}
		}
		return autoScreenDoneMsg{moved: len(moves)}
	}
}

func (m Model) screenerCmd(e *imap.Email, action string) tea.Cmd {
	folder := m.activeFolder()
	return func() tea.Msg {
		var dst string
		var addErr error
		switch action {
		case "I":
			addErr = m.screener.Approve(e.From)
			dst = m.cfg.Folders.Inbox
		case "O":
			addErr = m.screener.Block(e.From)
			dst = m.cfg.Folders.ScreenedOut
		case "F":
			addErr = m.screener.MarkFeed(e.From)
			dst = m.cfg.Folders.Feed
		case "P":
			addErr = m.screener.MarkPaperTrail(e.From)
			dst = m.cfg.Folders.PaperTrail
		case "$":
			addErr = m.screener.MarkSpam(e.From)
			dst = m.cfg.Folders.Spam
		}
		if addErr != nil {
			return errMsg{addErr}
		}
		if dst != "" && dst != folder {
			if _, err := m.imapCli().MoveMessage(nil, folder, e.UID, dst); err != nil {
				return errMsg{err}
			}
		}
		return screenDoneMsg{}
	}
}

// ── Update ────────────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.MouseMsg:
		if m.state == stateInbox && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && msg.Y == 0 {
			// Click on tab bar — compute zones and match.
			_, zones := folderTabs(m.folders, "", m.folderCounts)
			offX := 0
			if len(m.accounts) > 1 {
				offX = len("  "+m.activeAccount().Name+" ·") + 2
			}
			clickX := msg.X - offX
			for _, z := range zones {
				if clickX >= z.xStart && clickX < z.xEnd {
					if z.folderIndex == m.activeFolderI && m.offTabFolder == "" {
						return m, nil // already on this tab
					}
					m.activeFolderI = z.folderIndex
					m.offTabFolder = ""
					m.loading = true
					return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))
				}
			}
		}
		// Fall through — let other components handle mouse events (scroll, etc.)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		listH := msg.Height - 4
		if listH < 5 {
			listH = 5
		}
		if m.inbox.Width() == 0 {
			m.inbox = newInboxList(msg.Width, listH, m.cfg.Folders.Sent, m.cfg.Folders.Drafts)
		} else {
			m.inbox.SetSize(msg.Width, listH)
		}
		m.reader = newReader(msg.Width, msg.Height-3)
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case emailsLoadedMsg:
		m.loading = false
		m.emails = msg.emails
		m.markedUIDs = make(map[uint32]bool) // clear marks on folder reload
		m.filterActive = false
		m.filterText = ""
		sortCmd := m.sortEmails() // applies sort and sets list items

		// First-run welcome: show a brief intro popup.
		if config.IsFirstRun() {
			config.MarkWelcomeShown()
			m.state = stateWelcome
			return m, tea.Batch(sortCmd, m.fetchFolderCountsCmd())
		}

		// Auto-screen: silently apply screener moves on every inbox load.
		// In-memory classification is instant; already-screened senders won't
		// appear in inbox again so this is idempotent.
		// Controlled by ui.auto_screen_on_load (default true).
		// Skip when all screener lists are empty — otherwise every email would
		// be moved to ToScreen on first run, confusing new users.
		if msg.folder == m.cfg.Folders.Inbox && m.cfg.UI.AutoScreen() && !m.screener.IsEmpty() {
			if moves := m.previewAutoScreen(); len(moves) > 0 {
				m.loading = true
				m.bulkProgress = m.newBulkOp("Screening", len(moves))
				return m, tea.Batch(sortCmd, m.fetchFolderCountsCmd(), m.spinner.Tick, m.execAutoScreenCmd(moves))
			}
		}
		return m, tea.Batch(sortCmd, m.fetchFolderCountsCmd())

	case folderCountsMsg:
		m.folderCounts = msg.counts
		return m, nil

	case ensureFoldersDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.status = msg.err.Error()
			m.isError = true
			return m, nil
		}
		if len(msg.created) > 0 {
			m.status = fmt.Sprintf("Created %d folder(s): %s", len(msg.created), strings.Join(msg.created, ", "))
		}
		return m, nil

	case deleteAllReadyMsg:
		if len(msg.uids) == 0 {
			m.loading = false
			m.status = "Folder is already empty."
			return m, nil
		}
		m.loading = false
		m.pendingDeleteAll = &msg
		m.status = fmt.Sprintf("PERMANENTLY delete %d email(s) from %s?  · y to confirm, n to cancel", len(msg.uids), msg.folder)
		m.isError = true // red to make it stand out
		return m, nil

	case bodyLoadedMsg:
		m.loading = false
		m.openEmail = msg.email
		m.openBody = msg.body
		m.openHTMLBody = msg.rawHTML
		m.openWebURL = msg.webURL
		m.openAttachments = msg.attachments
		// Mark as seen in background (best-effort)
		uid := msg.email.UID
		folder := msg.email.Folder
		go func() { _ = m.imapCli().MarkSeen(nil, folder, uid) }()
		if m.pendingForward {
			m.pendingForward = false
			return m.launchForwardCmd()
		}
		if m.pendingReply {
			m.pendingReply = false
			return m.launchReplyCmd()
		}
		if m.pendingReplyAll {
			m.pendingReplyAll = false
			return m.launchReplyAllCmd()
		}
		m.openLinks = extractLinks(msg.body)
		_ = loadEmailIntoReader(&m.reader, msg.email, msg.body, msg.attachments, m.openLinks, m.cfg.UI.Theme, m.width)
		m.state = stateReading
		return m, nil

	case sendDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.status = msg.err.Error()
			m.isError = true
		} else if msg.warning != "" {
			m.status = msg.warning
			m.isError = true // show in red so user notices
			m.state = stateInbox
		} else {
			m.status = "Sent!"
			m.isError = false
			m.state = stateInbox
		}
		// Update local Answered flag so the reply indicator shows immediately.
		if msg.replyToUID > 0 {
			items := m.inbox.Items()
			for i, it := range items {
				if ei, ok := it.(emailItem); ok && ei.email.UID == msg.replyToUID {
					ei.email.Answered = true
					items[i] = ei
					break
				}
			}
			m.inbox.SetItems(items)
		}
		return m, nil

	case attachOpenDoneMsg:
		if msg.err != nil {
			m.status = "Attachment error: " + msg.err.Error()
			m.isError = true
		} else {
			m.status = "Saved to " + msg.path + " — opening…"
			m.isError = false
		}
		return m, nil

	case saveDraftDoneMsg:
		if msg.err != nil {
			m.status = "Draft error: " + msg.err.Error()
			m.isError = true
		} else {
			m.attachments = nil
			m.pendingSend = nil
			m.state = stateInbox
			m.status = "Saved to Drafts."
			m.isError = false
		}
		return m, nil

	case screenDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.status = msg.err.Error()
			m.isError = true
			return m, nil
		}
		m.status = "Done."
		m.isError = false
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))

	case toggleSeenDoneMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			m.isError = true
			return m, nil
		}
		// Update local seen state so the N flag flips immediately
		for i := range m.emails {
			if m.emails[i].UID == msg.uid {
				m.emails[i].Seen = msg.seen
				break
			}
		}
		return m, m.applyFilter()

	case imapSearchResultMsg:
		return m.handleIMAPSearchResult(msg)

	case everythingResultMsg:
		return m.handleEverythingResult(msg)

	case conversationResultMsg:
		return m.handleConversationResult(msg)

	case batchDoneMsg:
		m.loading = false
		m.bulkProgress = nil
		m.markedUIDs = make(map[uint32]bool)
		if msg.err != nil {
			// Include partial undo info so user can reverse already-moved emails.
			if len(msg.undo) > 0 {
				m.undoStack = append(m.undoStack, msg.undo)
				if len(m.undoStack) > maxUndoStack {
					m.undoStack = m.undoStack[len(m.undoStack)-maxUndoStack:]
				}
			}
			m.status = msg.err.Error()
			m.isError = true
			return m, nil
		}
		if len(msg.undo) > 0 {
			m.undoStack = append(m.undoStack, msg.undo)
			if len(m.undoStack) > maxUndoStack {
				m.undoStack = m.undoStack[len(m.undoStack)-maxUndoStack:]
			}
		}
		m.status = "Done."
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))

	case moveDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.status = msg.err.Error()
			m.isError = true
			return m, nil
		}
		if len(msg.undo) > 0 {
			m.undoStack = append(m.undoStack, msg.undo)
			if len(m.undoStack) > maxUndoStack {
				m.undoStack = m.undoStack[len(m.undoStack)-maxUndoStack:]
			}
		}
		m.status = "Moved."
		m.isError = false
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))

	case undoDoneMsg:
		m.loading = false
		m.status = "Undone."
		m.isError = false
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))

	case deepScreenCountMsg:
		// Phase 1 done: we know how many emails exist. Show count and kick off phase 2.
		m.status = fmt.Sprintf("Screen-all: found %d emails — fetching headers in batches…", msg.total)
		return m, tea.Batch(m.spinner.Tick, m.deepScreenClassifyCmd(nil, msg.uids, msg.total))

	case deepScreenBatchMsg:
		// One batch done — show progress, kick off next batch or classify.
		fetched := len(msg.emails)
		if len(msg.remaining) > 0 {
			m.status = fmt.Sprintf("Screen-all: fetched %d/%d emails…", fetched, msg.total)
			return m, tea.Batch(m.spinner.Tick, m.deepScreenClassifyCmd(msg.emails, msg.remaining, msg.total))
		}
		// All batches done — classify in-memory (O(1) map lookups).
		inboxFolder := m.cfg.Folders.Inbox
		var moves []autoScreenMove
		for i := range msg.emails {
			e := &msg.emails[i]
			cat := m.screener.Classify(e.From)
			var dst string
			switch cat {
			case screener.CategorySpam:
				dst = m.cfg.Folders.Spam
			case screener.CategoryScreenedOut:
				dst = m.cfg.Folders.ScreenedOut
			case screener.CategoryFeed:
				dst = m.cfg.Folders.Feed
			case screener.CategoryPaperTrail:
				dst = m.cfg.Folders.PaperTrail
			case screener.CategoryToScreen:
				dst = m.cfg.Folders.ToScreen
			}
			if dst != "" && dst != inboxFolder {
				moves = append(moves, autoScreenMove{email: e, dst: dst})
			}
		}
		m.loading = false
		if len(moves) == 0 {
			m.status = fmt.Sprintf("Screen-all: all %d inbox emails already classified.", msg.total)
			return m, nil
		}
		counts := map[string]int{}
		for _, mv := range moves {
			counts[mv.dst]++
		}
		summary := fmt.Sprintf("Screen-all: %d/%d email(s) to move:", len(moves), msg.total)
		for dst, n := range counts {
			summary += fmt.Sprintf(" %d→%s", n, dst)
		}
		summary += "  · y to apply, n to cancel"
		m.pendingMoves = moves
		m.status = summary
		return m, nil

	case deepScreenReadyMsg:
		m.loading = false
		if len(msg.moves) == 0 {
			m.status = fmt.Sprintf("Deep screen: all %d inbox emails already classified.", msg.total)
			return m, nil
		}
		counts := map[string]int{}
		for _, mv := range msg.moves {
			counts[mv.dst]++
		}
		summary := fmt.Sprintf("Deep screen %d/%d email(s):", len(msg.moves), msg.total)
		for dst, n := range counts {
			summary += fmt.Sprintf(" %d→%s", n, dst)
		}
		summary += "  · y to apply, n to cancel"
		m.pendingMoves = msg.moves
		m.status = summary
		return m, nil

	case resetToScreenReadyMsg:
		if len(msg.uids) == 0 {
			m.loading = false
			m.status = "ToScreen is already empty."
			return m, nil
		}
		m.loading = false
		m.pendingResetUIDs = msg.uids
		m.status = fmt.Sprintf("Move %d email(s) from ToScreen → Inbox?  · y to apply, n to cancel", len(msg.uids))
		return m, nil

	case autoScreenDoneMsg:
		m.loading = false
		m.bulkProgress = nil
		if msg.err != nil {
			m.status = fmt.Sprintf("Screening stopped after %d: %s", msg.moved, msg.err)
			m.isError = true
			return m, nil
		}
		m.status = fmt.Sprintf("Screened %d email(s).", msg.moved)
		m.isError = false
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))

	case bgSyncTickMsg:
		// Fire background inbox fetch; reschedule next tick in parallel.
		return m, tea.Batch(m.bgFetchInboxCmd(), m.scheduleBgSync())

	case bgInboxFetchedMsg:
		moves := m.classifyForScreen(msg.emails)
		if len(moves) == 0 {
			return m, nil
		}
		return m, m.bgExecAutoScreenCmd(moves)

	case bgScreenDoneMsg:
		if msg.moved > 0 {
			if msg.moved < msg.total {
				m.status = fmt.Sprintf("Background sync: screened %d/%d — press R to retry", msg.moved, msg.total)
				m.isError = true
			}
			// Refresh the visible folder so the user sees the clean result.
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))
		}
		return m, nil

	case errMsg:
		m.loading = false
		m.status = msg.err.Error()
		m.isError = true
		return m, nil

	case editorDoneMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			m.isError = true
			m.state = stateInbox
			return m, nil
		}
		if msg.aborted {
			m.status = "Aborted (no changes saved)."
			m.state = stateInbox
			return m, nil
		}
		if strings.TrimSpace(msg.body) == "" {
			m.status = "Cancelled (empty body)."
			m.state = stateInbox
			return m, nil
		}
		// Strip editor header hints and extract [attach] lines.
		inlineAttach, cleanBody := extractInlineAttachments(stripPrelude(msg.body))
		m.attachments = append(m.attachments, inlineAttach...)

		// Go to pre-send review instead of sending immediately.
		m.pendingSend = &pendingSendData{
			to: msg.to, cc: msg.cc, bcc: mergeAutoBCC(msg.bcc, m.cfg.AutoBCC),
			subject: msg.subject, body: cleanBody,
		}
		// Track original email for \Answered flag (replies/forwards).
		if m.openEmail != nil && strings.HasPrefix(strings.ToLower(msg.subject), "re:") {
			m.pendingSend.replyToUID = m.openEmail.UID
			m.pendingSend.replyToFolder = m.openEmail.Folder
		}
		m.state = statePresend
		return m, nil

	case attachPickDoneMsg:
		m.attachments = append(m.attachments, msg.paths...)
		if len(msg.paths) > 0 {
			m.status = fmt.Sprintf("Attached %d file(s).", len(msg.paths))
		}
		return m, nil

	case tea.KeyMsg:
		// ? opens help from any state; q/esc/? closes it
		if msg.String() == "?" {
			if m.state == stateHelp {
				m.state = m.prevState
			} else {
				m.prevState = m.state
				m.state = stateHelp
			}
			return m, nil
		}
		switch m.state {
		case stateInbox:
			return m.updateInbox(msg)
		case stateReading:
			return m.updateReader(msg)
		case stateCompose:
			return m.updateCompose(msg)
		case statePresend:
			return m.updatePresend(msg)
		case stateHelp:
			return m.updateHelp(msg)
		case stateWelcome:
			// Any key dismisses the welcome popup
			m.state = stateInbox
			return m, nil
		}
	}

	return m, nil
}

func (m Model) updateInbox(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// ── Vim-style ":" command line ──────────────────────────────────
	if m.cmdMode {
		switch key {
		case "esc":
			m.cmdMode = false
			m.cmdText = ""
			m.cmdHistI = -1
		case "enter":
			m.cmdMode = false
			m.cmdHistI = -1
			input := strings.TrimSpace(m.cmdText)
			m.cmdText = ""
			if input != "" {
				m.cmdHistory = addCmdHistory(m.cmdHistory, input)
				go saveCmdHistory(config.HistoryPath(), m.cmdHistory)
			}
			if cmd := matchCmd(input); cmd != nil {
				result, c := cmd.run(&m)
				return result, c
			}
			if input != "" {
				m.status = "Unknown command: " + input
				m.isError = true
			}
		case "up":
			if len(m.cmdHistory) > 0 {
				m.cmdHistI++
				if m.cmdHistI >= len(m.cmdHistory) {
					m.cmdHistI = len(m.cmdHistory) - 1
				}
				m.cmdText = m.cmdHistory[m.cmdHistI]
				m.cmdTabI = 0
			}
		case "down":
			if m.cmdHistI > 0 {
				m.cmdHistI--
				m.cmdText = m.cmdHistory[m.cmdHistI]
			} else {
				m.cmdHistI = -1
				m.cmdText = ""
			}
			m.cmdTabI = 0
		case "backspace", "ctrl+h":
			runes := []rune(m.cmdText)
			if len(runes) > 0 {
				m.cmdText = string(runes[:len(runes)-1])
			}
			m.cmdTabI = 0
			m.cmdHistI = -1
		case "right": // accept ghost completion (first match)
			if first := matchCmd(m.cmdText); first != nil {
				m.cmdText = first.name
				m.cmdTabI = 0
			}
		case "tab", "ctrl+n": // cycle forward through completions
			matches := matchCmds(m.cmdText)
			if len(matches) > 0 {
				m.cmdText = matches[m.cmdTabI%len(matches)].name
				m.cmdTabI++
			}
		case "ctrl+p": // cycle backward through completions
			matches := matchCmds(m.cmdText)
			if len(matches) > 0 {
				m.cmdTabI = (m.cmdTabI - 2 + len(matches)) % len(matches)
				m.cmdText = matches[m.cmdTabI].name
				m.cmdTabI++
			}
		default:
			if len(key) == 1 {
				m.cmdText += key
				m.cmdTabI = 0 // reset cycle on new input
				m.cmdHistI = -1
			}
		}
		return m, nil
	}

	// ── IMAP server-side search mode ────────────────────────────────
	if m.imapSearchActive {
		mm, cmd, consumed := m.updateIMAPSearch(key)
		if consumed {
			return mm, cmd
		}
	}

	// ── Our own filter mode ─────────────────────────────────────────
	// When active, consume all keys for text input; no inbox commands fire.
	if m.filterActive {
		switch key {
		case "esc":
			m.filterActive = false
			m.filterText = ""
			return m, m.applyFilter()
		case "enter":
			m.filterActive = false // commit filter, keep results
			return m, nil
		case "backspace", "ctrl+h":
			runes := []rune(m.filterText)
			if len(runes) > 0 {
				m.filterText = string(runes[:len(runes)-1])
			}
			return m, m.applyFilter()
		default:
			if len(key) == 1 {
				m.filterText += key
				return m, m.applyFilter()
			}
		}
		return m, nil
	}

	// Handle pending chord prefix (g or M) — consume the second key
	if m.pendingKey != "" {
		prefix := m.pendingKey
		m.pendingKey = ""
		m.status = ""
		m.isError = false
		return m.handleChord(prefix, key)
	}

	// Clear pending confirmations on any key except y/n
	if key != "y" && key != "n" {
		m.pendingMoves = nil
		m.pendingResetUIDs = nil
		m.pendingDeleteAll = nil
	}
	m.status = ""
	m.isError = false

	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit

	case "esc":
		if m.imapSearchResults {
			m.imapSearchResults = false
			m.imapSearchText = ""
			m.offTabFolder = ""
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))
		}

	// ── Chord prefixes ──────────────────────────────────────────────
	case "g":
		m.pendingKey = "g"
		m.status = "go to:  gi inbox  ga archive  gf feed  gp papertrail  gt trash  gs sent  gk toscreen  go screened-out  gw waiting  gm someday  gd drafts  gS spam  ge everything  gg top"
		return m, nil

	case " ": // leader key — wait for digit or shortcut
		m.pendingKey = " "
		m.status = "leader:  1-9 folder tab  / IMAP search  (esc to cancel)"
		return m, nil

	case "M":
		m.pendingKey = "M"
		m.status = "move to:  Mi inbox  Ma archive  Mf feed  Mp papertrail  Mt trash  Mo screened-out  Mw waiting  Mm someday"
		return m, nil

	case ",":
		m.pendingKey = ","
		m.status = "sort:  ,m date↓  ,M date↑  ,a from A-Z  ,A from Z-A  ,s size↑  ,S size↓  ,n subject A-Z  ,N subject Z-A"
		return m, nil

	// ── Mark for batch / delete ─────────────────────────────────────
	case "x":
		targets := m.targetEmails()
		if len(targets) == 0 {
			return m, nil
		}
		m.loading = true
		m.bulkProgress = m.newBulkOp("Deleting", len(targets))
		return m, tea.Batch(m.spinner.Tick, m.batchMoveCmd(targets, m.cfg.Folders.Trash))

	case "X": // permanent delete (marked or cursor) — only in Trash
		if m.activeFolder() != m.cfg.Folders.Trash {
			m.status = "X only works in Trash. Use x to move to Trash first."
			m.isError = true
			return m, nil
		}
		targets := m.targetEmails()
		if len(targets) == 0 {
			return m, nil
		}
		var uids []uint32
		for _, e := range targets {
			uids = append(uids, e.UID)
		}
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.deleteAllExecCmd(m.cfg.Folders.Trash, uids))

	case "ctrl+u": // clear all marks
		m.markedUIDs = make(map[uint32]bool)
		return m, m.applyFilter()

	case "U": // undo last move/delete
		if len(m.undoStack) == 0 {
			m.status = "Nothing to undo."
			return m, nil
		}
		last := m.undoStack[len(m.undoStack)-1]
		m.undoStack = m.undoStack[:len(m.undoStack)-1]
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.undoMovesCmd(last))

	// ── Screener actions — operate on marked emails or cursor email ──
	case "I", "O", "F", "P", "$":
		targets := m.targetEmails()
		if len(targets) == 0 {
			return m, nil
		}
		m.loading = true
		m.bulkProgress = m.newBulkOp("Screening", len(targets))
		return m, tea.Batch(m.spinner.Tick, m.batchScreenerCmd(targets, key))

	// A = archive (pure move, no screener update)
	case "A":
		targets := m.targetEmails()
		if len(targets) == 0 {
			return m, nil
		}
		m.loading = true
		m.bulkProgress = m.newBulkOp("Archiving", len(targets))
		return m, tea.Batch(m.spinner.Tick, m.batchMoveCmd(targets, m.cfg.Folders.Archive))

	// ── Auto-screen dry-run (Inbox only) ────────────────────────────
	case ":":
		m.cmdMode = true
		m.cmdText = ""
		m.cmdHistI = -1
		return m, nil

	case "S":
		if m.folders[m.activeFolderI] != "Inbox" {
			break
		}
		moves := m.previewAutoScreen()
		if len(moves) == 0 {
			m.status = "Nothing to screen — all senders already classified."
			return m, nil
		}
		counts := map[string]int{}
		for _, mv := range moves {
			counts[mv.dst]++
		}
		summary := fmt.Sprintf("Would move %d email(s):", len(moves))
		for dst, n := range counts {
			summary += fmt.Sprintf(" %d→%s", n, dst)
		}
		summary += "  · y to apply, n to cancel"
		m.pendingMoves = moves
		m.status = summary
		return m, nil

	case "y":
		if m.pendingDeleteAll != nil {
			p := m.pendingDeleteAll
			m.pendingDeleteAll = nil
			m.isError = false
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.deleteAllExecCmd(p.folder, p.uids))
		}
		if len(m.pendingResetUIDs) > 0 {
			uids := m.pendingResetUIDs
			m.pendingResetUIDs = nil
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.resetToScreenMoveCmd(uids))
		}
		if len(m.pendingMoves) == 0 {
			break
		}
		moves := m.pendingMoves
		m.pendingMoves = nil
		m.loading = true
		m.bulkProgress = m.newBulkOp("Screening", len(moves))
		return m, tea.Batch(m.spinner.Tick, m.execAutoScreenCmd(moves))

	case "n":
		if m.pendingDeleteAll != nil || len(m.pendingResetUIDs) > 0 || len(m.pendingMoves) > 0 {
			m.pendingDeleteAll = nil
			m.pendingResetUIDs = nil
			m.pendingMoves = nil
			m.isError = false
			m.status = "Cancelled."
			return m, nil
		}
		// No pending confirmation — toggle read/unread
		targets := m.targetEmails()
		if len(targets) == 0 {
			break
		}
		if len(targets) == 1 && len(m.markedUIDs) == 0 {
			next := m.inbox.Index() + 1
			if next < len(m.inbox.Items()) {
				m.inbox.Select(next)
			}
			return m, m.toggleSeenCmd(&targets[0])
		}
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.batchToggleSeenCmd(targets))

	// ── Navigation ──────────────────────────────────────────────────
	case "tab", "L", "]":
		m.activeFolderI = (m.activeFolderI + 1) % len(m.folders)
		m.offTabFolder = ""
		m.imapSearchResults = false
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))

	case "shift+tab", "H", "[":
		m.activeFolderI = (m.activeFolderI - 1 + len(m.folders)) % len(m.folders)
		m.offTabFolder = ""
		m.imapSearchResults = false
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))

	case "G":
		m.inbox.Select(len(m.inbox.Items()) - 1)
		return m, nil

	case "/":
		m.filterActive = true
		m.filterText = ""
		return m, m.applyFilter()

	case "ctrl+n": // mark all loaded emails in this folder as read
		cmd := m.markAllSeenCmd()
		if cmd == nil {
			m.status = "All already read."
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, cmd)

	case "ctrl+a":
		if len(m.clients) > 1 {
			m.accountI = (m.accountI + 1) % len(m.clients)
			m.activeFolderI = 0
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))
		}

	case "c":
		m.state = stateCompose
		m.compose.reset()
		m.presendFromI = 0
		return m, nil

	case "R":
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))

	case "enter", "l":
		e := selectedEmail(m.inbox)
		if e == nil {
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchBodyCmd(e))

	case "r":
		e := selectedEmail(m.inbox)
		if e == nil {
			return m, nil
		}
		// Pre-select the correct From address before fetching body.
		if idx := m.matchFromIndex(e.To, e.CC); idx >= 0 {
			m.presendFromI = idx
		}
		m.pendingReply = true
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchBodyCmd(e))

	case "ctrl+r":
		e := selectedEmail(m.inbox)
		if e == nil {
			return m, nil
		}
		if idx := m.matchFromIndex(e.To, e.CC); idx >= 0 {
			m.presendFromI = idx
		}
		m.pendingReplyAll = true
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchBodyCmd(e))

	case "f":
		e := selectedEmail(m.inbox)
		if e == nil {
			return m, nil
		}
		m.pendingForward = true
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchBodyCmd(e))

	case "T":
		e := selectedEmail(m.inbox)
		if e == nil {
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchConversationCmd(e))

	case "m": // mark/unmark current email for batch, advance cursor
		e := selectedEmail(m.inbox)
		if e == nil {
			break
		}
		if m.markedUIDs[e.UID] {
			delete(m.markedUIDs, e.UID)
		} else {
			m.markedUIDs[e.UID] = true
		}
		next := m.inbox.Index() + 1
		if next < len(m.inbox.Items()) {
			m.inbox.Select(next)
		}
		return m, m.applyFilter()

	}

	// Forward remaining keys (j/k navigation, filter /) to list
	var cmd tea.Cmd
	m.inbox, cmd = m.inbox.Update(msg)
	return m, cmd
}

// sortEmails sorts m.emails in place according to m.sortField / m.sortReverse,
// then refreshes the list widget.
func (m *Model) sortEmails() tea.Cmd {
	field, rev := m.sortField, m.sortReverse
	sort.SliceStable(m.emails, func(i, j int) bool {
		a, b := m.emails[i], m.emails[j]
		var less bool
		switch field {
		case "from":
			less = strings.ToLower(a.From) < strings.ToLower(b.From)
		case "subject":
			less = strings.ToLower(a.Subject) < strings.ToLower(b.Subject)
		case "size":
			less = a.Size < b.Size
		default: // "date"
			less = a.Date.Before(b.Date)
		}
		if rev {
			return !less
		}
		return less
	})
	return m.applyFilter()
}

// loadCmdHistory reads persisted command history from path (newest first).
// Returns nil on any error so startup is never blocked.
func loadCmdHistory(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// saveCmdHistory writes history to path (one entry per line, newest first).
// Called in a goroutine — errors are silently ignored.
func saveCmdHistory(path string, history []string) {
	content := strings.Join(history, "\n") + "\n"
	_ = os.WriteFile(path, []byte(content), 0600)
}

// addCmdHistory prepends input to history (deduplicating) and caps at 5 entries.
func addCmdHistory(history []string, input string) []string {
	// Remove existing occurrence of the same command (dedup)
	out := history[:0:len(history)]
	for _, h := range history {
		if h != input {
			out = append(out, h)
		}
	}
	// Prepend newest entry
	result := make([]string, 1, len(out)+1)
	result[0] = input
	result = append(result, out...)
	if len(result) > 5 {
		result = result[:5]
	}
	return result
}

// applyFilter filters m.emails by filterText and refreshes the list.
// Call this whenever filterText changes.
func (m *Model) applyFilter() tea.Cmd {
	if m.filterText == "" {
		return setEmails(&m.inbox, m.emails, m.markedUIDs)
	}
	query := strings.ToLower(m.filterText)
	var filtered []imap.Email
	for _, e := range m.emails {
		hay := strings.ToLower(e.From + " " + e.Subject)
		if strings.Contains(hay, query) {
			filtered = append(filtered, e)
		}
	}
	return setEmails(&m.inbox, filtered, m.markedUIDs)
}

// handleChord dispatches two-key sequences (g<x>, M<x>, space<x>).
func (m Model) handleChord(prefix, key string) (tea.Model, tea.Cmd) {
	switch prefix {
	case " ": // leader key — digit jumps to folder tab (1-based)
		if key == "/" {
			m.imapSearchActive = true
			m.imapSearchText = ""
			m.imapSearchResults = false
			return m, nil
		}
		if len(key) == 1 && key >= "1" && key <= "9" {
			idx := int(key[0] - '1') // 0-based
			if idx < len(m.folders) {
				if idx == m.activeFolderI {
					return m, nil
				}
				m.activeFolderI = idx
				m.offTabFolder = ""
				m.loading = true
				return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))
			}
		}
		if key != "esc" {
			m.status = fmt.Sprintf("leader: unknown key %q", key)
		}
		return m, nil

	case "g":
		if key == "g" { // gg = top of list
			m.inbox.Select(0)
			return m, nil
		}
		if key == "S" { // gS — go to Spam (not in tab rotation)
			m.loading = true
			m.offTabFolder = "Spam"
			m.status = "Spam folder — press R to reload, tab to leave"
			return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.cfg.Folders.Spam))
		}
		if key == "d" { // gd — go to Drafts (not in tab rotation)
			m.loading = true
			m.offTabFolder = "Drafts"
			m.status = "Drafts folder — press R to reload, tab to leave"
			return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.cfg.Folders.Drafts))
		}
		if key == "e" { // ge — Everything: latest emails across all folders
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.fetchEverythingCmd())
		}
		folderMap := map[string]string{
			"i": "Inbox",
			"f": "Feed",
			"p": "PaperTrail",
			"t": "Trash",
			"s": "Sent",
			"k": "ToScreen",
			"a": "Archive",
			"w": "Waiting",
			"b": "Work",
			"m": "Someday",
			"o": "ScreenedOut",
		}
		if name, ok := folderMap[key]; ok {
			for i, f := range m.folders {
				if f == name {
					if i == m.activeFolderI && m.offTabFolder == "" {
						return m, nil
					}
					m.activeFolderI = i
					m.offTabFolder = ""
					m.loading = true
					return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))
				}
			}
		}
		m.status = fmt.Sprintf("unknown: g%s", key)

	case "M":
		targets := m.targetEmails()
		if len(targets) == 0 {
			return m, nil
		}
		dstMap := map[string]string{
			"i": m.cfg.Folders.Inbox,
			"a": m.cfg.Folders.Archive,
			"f": m.cfg.Folders.Feed,
			"p": m.cfg.Folders.PaperTrail,
			"t": m.cfg.Folders.Trash,
			"o": m.cfg.Folders.ScreenedOut,
			"w": m.cfg.Folders.Waiting,
			"b": m.cfg.Folders.Work,
			"m": m.cfg.Folders.Someday,
			"k": m.cfg.Folders.ToScreen,
		}
		if dst, ok := dstMap[key]; ok {
			m.loading = true
			m.bulkProgress = m.newBulkOp("Moving", len(targets))
			return m, tea.Batch(m.spinner.Tick, m.batchMoveCmd(targets, dst))
		}
		m.status = fmt.Sprintf("unknown: M%s", key)

	case ",":
		type sortSpec struct {
			field string
			rev   bool
		}
		specs := map[string]sortSpec{
			"m": {"date", true},
			"M": {"date", false},
			"a": {"from", false},
			"A": {"from", true},
			"s": {"size", false},
			"S": {"size", true},
			"n": {"subject", false},
			"N": {"subject", true},
		}
		if sp, ok := specs[key]; ok {
			m.sortField, m.sortReverse = sp.field, sp.rev
			label := map[string]string{
				"m": "date ↓ (newest first)",
				"M": "date ↑ (oldest first)",
				"a": "from A→Z",
				"A": "from Z→A",
				"s": "size ↑ (smallest first)",
				"S": "size ↓ (largest first)",
				"n": "subject A→Z",
				"N": "subject Z→A",
			}[key]
			m.status = "Sort: " + label
			return m, m.sortEmails()
		}
		m.status = fmt.Sprintf("unknown: ,%s", key)
	}
	return m, nil
}

func (m Model) updateReader(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Handle reader chords
	if m.readerPending != "" {
		pending := m.readerPending
		m.readerPending = ""
		switch pending {
		case " ": // space + digit opens link
			if len(key) == 1 && key >= "0" && key <= "9" {
				var idx int
				if key == "0" {
					idx = 9
				} else {
					idx = int(key[0] - '1')
				}
				if idx < len(m.openLinks) {
					return m, m.openLinkCmd(m.openLinks[idx].URL)
				}
				m.status = fmt.Sprintf("No link [%s].", key)
				return m, nil
			}
			// Not a digit — fall through
		case "g": // gg = top of email
			if key == "g" {
				m.reader.GotoTop()
				return m, nil
			}
		}
	}

	switch key {
	case "q", "esc", "h":
		m.state = stateInbox
		m.readerPending = ""
		return m, nil
	case "e":
		return m.openInNeovim()
	case "E":
		return m.continueDraft()
	case "o":
		return m.openInW3m()
	case "O":
		return m.openInBrowser()
	case "ctrl+o":
		return m.openWebVersion()
	case "r":
		if m.openEmail != nil {
			return m.launchReplyCmd()
		}
	case "R":
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))
	case "ctrl+r":
		if m.openEmail != nil {
			return m.launchReplyAllCmd()
		}
	case "f":
		if m.openEmail != nil {
			return m.launchForwardCmd()
		}
	case "T":
		if m.openEmail != nil {
			m.loading = true
			m.state = stateInbox
			return m, tea.Batch(m.spinner.Tick, m.fetchConversationCmd(m.openEmail))
		}
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx := int(msg.String()[0] - '1') // 0-based
		if idx < len(m.openAttachments) {
			return m, m.downloadOpenAttachmentCmd(m.openAttachments[idx])
		}
	case " ":
		if len(m.openLinks) > 0 {
			m.readerPending = " "
			m.status = "open link: space+1-9 (0=10th)"
			return m, nil
		}
	case "g":
		m.readerPending = "g"
		return m, nil
	case "G":
		m.reader.GotoBottom()
		return m, nil
	}
	var cmd tea.Cmd
	m.reader, cmd = m.reader.Update(msg)
	return m, cmd
}

// openLinkCmd opens a URL in $BROWSER (or xdg-open).
func (m Model) openLinkCmd(url string) tea.Cmd {
	browser := os.Getenv("BROWSER")
	if browser == "" {
		browser = "xdg-open"
	}
	return func() tea.Msg {
		cmd := exec.Command(browser, url)
		_ = cmd.Start()
		return nil
	}
}

// openInBrowser writes the email as HTML to a temp file and opens it in
// $BROWSER (xdg-open as fallback). Uses cmd.Start() — not ExecProcess — because
// GUI browsers (and xdg-open) exit immediately after handing off; ExecProcess
// would delete the temp file before the browser has loaded it.
func (m Model) openInBrowser() (tea.Model, tea.Cmd) {
	if m.openBody == "" {
		return m, nil
	}

	var htmlBody string
	if m.openHTMLBody != "" {
		htmlBody = m.openHTMLBody
	} else {
		var err error
		htmlBody, err = render.ToHTML(m.openBody)
		if err != nil {
			htmlBody = "<pre>" + m.openBody + "</pre>"
		}
	}

	f, err := os.CreateTemp(neomdTempDir(), "neomd-view-*.html")
	if err != nil {
		m.status = "open: " + err.Error()
		m.isError = true
		return m, nil
	}
	tmpPath := f.Name()
	f.WriteString(htmlBody) //nolint
	f.Close()

	browser := os.Getenv("BROWSER")
	if browser == "" {
		browser = "xdg-open"
	}

	return m, func() tea.Msg {
		cmd := exec.Command(browser, tmpPath)
		_ = cmd.Start()
		// xdg-open exits immediately after handing off to the browser process,
		// so cmd.Wait() returns before the browser has read the file.
		// Sleep long enough for any browser to finish loading from disk.
		go func() {
			time.Sleep(15 * time.Second)
			os.Remove(tmpPath)
		}()
		return nil
	}
}

// openInW3m writes the email as HTML to a temp file and opens it in w3m.
// w3m is a TUI process so ExecProcess (suspend/resume) is correct here.
func (m Model) openInW3m() (tea.Model, tea.Cmd) {
	if m.openBody == "" {
		return m, nil
	}

	var htmlBody string
	if m.openHTMLBody != "" {
		htmlBody = m.openHTMLBody
	} else {
		var err error
		htmlBody, err = render.ToHTML(m.openBody)
		if err != nil {
			htmlBody = "<pre>" + m.openBody + "</pre>"
		}
	}

	f, err := os.CreateTemp(neomdTempDir(), "neomd-view-*.html")
	if err != nil {
		m.status = "open: " + err.Error()
		m.isError = true
		return m, nil
	}
	tmpPath := f.Name()
	f.WriteString(htmlBody) //nolint
	f.Close()

	cmd := exec.Command("w3m", tmpPath)
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		os.Remove(tmpPath)
		if err != nil {
			return errMsg{err}
		}
		return nil
	})
}

// openWebVersion opens the canonical "view online" URL for this email in $BROWSER.
// URL is extracted at fetch time from the List-Post header or plain-text preamble
// (Substack: "View this post on the web at …"). Falls back to HTML anchor scan.
func (m Model) openWebVersion() (tea.Model, tea.Cmd) {
	url := m.openWebURL
	if url == "" {
		url = extractWebVersionURL(m.openHTMLBody) // HTML anchor scan as last resort
	}
	if url == "" {
		m.status = "No web version link found in this email."
		return m, nil
	}
	lower := strings.ToLower(url)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		m.status = "Blocked: URL has unsafe scheme."
		return m, nil
	}

	browser := os.Getenv("BROWSER")
	if browser == "" {
		browser = "xdg-open"
	}
	return m, func() tea.Msg {
		_ = exec.Command(browser, url).Start()
		return nil
	}
}

// downloadOpenAttachmentCmd saves the attachment to ~/Downloads and opens it
// with xdg-open (non-blocking — does not suspend the TUI).
func (m Model) downloadOpenAttachmentCmd(a imap.Attachment) tea.Cmd {
	return func() tea.Msg {
		home, err := os.UserHomeDir()
		if err != nil {
			return attachOpenDoneMsg{err: err}
		}
		dir := filepath.Join(home, "Downloads")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return attachOpenDoneMsg{err: fmt.Errorf("create Downloads: %w", err)}
		}
		// Avoid overwriting existing files by appending a counter before the extension.
		base := filepath.Base(a.Filename)
		dst := filepath.Join(dir, base)
		if _, err := os.Stat(dst); err == nil {
			ext := filepath.Ext(base)
			name := base[:len(base)-len(ext)]
			for i := 1; ; i++ {
				dst = filepath.Join(dir, fmt.Sprintf("%s_%d%s", name, i, ext))
				if _, err := os.Stat(dst); os.IsNotExist(err) {
					break
				}
			}
		}
		if err := os.WriteFile(dst, a.Data, 0644); err != nil {
			return attachOpenDoneMsg{err: fmt.Errorf("save attachment: %w", err)}
		}
		_ = exec.Command("xdg-open", dst).Start()
		return attachOpenDoneMsg{path: dst}
	}
}

// extractWebVersionURL looks for the "view in browser" / "read online" link
// that newsletter platforms insert near the top of every HTML email.
// Searches only the first 3000 bytes (the link is always in the preheader).
func extractWebVersionURL(body string) string {
	// Limit search to the top of the email where "view online" links live.
	top := body
	if len(top) > 3000 {
		top = top[:3000]
	}

	// Anchor text patterns used by major platforms:
	//   "View in browser"      — Mailchimp, generic
	//   "View online"          — many platforms
	//   "Read online"          — ConvertKit, generic
	//   "Read on Substack"     — Substack
	//   "Read on Beehiiv"      — Beehiiv
	//   "Open in browser"      — Ghost
	//   "View web version"     — generic
	//   "View this email"      — Mailchimp variant
	re := regexp.MustCompile(`(?i)<a[^>]+href=["']([^"'#][^"']*?)["'][^>]*>\s*(?:[^<]*\s)?(?:view|read|open|see)\b[^<]*</a>`)
	for _, m := range re.FindAllStringSubmatch(top, -1) {
		u := m[1]
		if strings.HasPrefix(u, "http") {
			return u
		}
	}
	return ""
}

// openInNeovim opens the current email's markdown body in nvim -R (read-only)
// so the user can search, copy, and navigate with full vim motions.
func (m Model) openInNeovim() (tea.Model, tea.Cmd) {
	if m.openEmail == nil || m.openBody == "" {
		return m, nil
	}

	// Build a header block so the file is self-contained in neovim.
	e := m.openEmail
	header := fmt.Sprintf("---\nFrom:    %s\nTo:      %s\nSubject: %s\nDate:    %s\n---\n\n",
		e.From, e.To, e.Subject, e.Date.Format("Mon, 02 Jan 2006 15:04:05 -0700"))

	cmd, tmpPath, err := editor.View(header + m.openBody)
	if err != nil {
		m.status = "nvim: " + err.Error()
		m.isError = true
		return m, nil
	}
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		os.Remove(tmpPath)
		if err != nil {
			return errMsg{err}
		}
		return nil
	})
}

// continueDraft opens the current email as an editable compose session,
// pre-filling To/CC/Subject and body from the saved draft. Saving in the
// editor goes through the normal pre-send review (enter to send, d to re-save).
func (m Model) continueDraft() (tea.Model, tea.Cmd) {
	if m.openEmail == nil {
		return m, nil
	}
	e := m.openEmail
	to := e.To
	cc := e.CC
	subject := e.Subject

	// Pre-fill compose fields so viewCompose shows them
	m.compose.reset()
	m.presendFromI = 0
	m.compose.to.SetValue(to)
	m.compose.cc.SetValue(cc)
	m.compose.subject.SetValue(subject)
	if cc != "" {
		m.compose.extraVisible = true
	}
	m.compose.step = 3 // jump past header steps to subject-done state

	// Build temp file with prelude + existing body.
	// No signature — the draft body already contains it from the first compose.
	prelude := editor.Prelude(to, cc, subject, "")
	body := m.openBody

	f, err := os.CreateTemp(neomdTempDir(), "neomd-*.md")
	if err != nil {
		m.status = "continueDraft: " + err.Error()
		m.isError = true
		return m, nil
	}
	tmpPath := f.Name()
	f.WriteString(prelude + body) //nolint
	f.Close()

	editorBin := os.Getenv("EDITOR")
	if editorBin == "" {
		editorBin = "nvim"
	}
	cmd := exec.Command(editorBin, tmpPath)
	draftBackups := m.cfg.UI.DraftBackups()
	m.state = stateCompose
	return m, tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		backupDraft(tmpPath, draftBackups)
		defer os.Remove(tmpPath)
		if execErr != nil {
			return editorDoneMsg{err: execErr}
		}
		raw, readErr := os.ReadFile(tmpPath)
		if readErr != nil {
			return editorDoneMsg{err: readErr}
		}
		if string(raw) == prelude+body {
			return editorDoneMsg{aborted: true}
		}
		pto, pcc, _, psubject, _ := editor.ParseHeaders(string(raw))
		if pto == "" {
			pto = to
		}
		if pcc == "" {
			pcc = cc
		}
		if psubject == "" {
			psubject = subject
		}
		return editorDoneMsg{to: pto, cc: pcc, bcc: "", subject: psubject, body: string(raw)}
	})
}

func (m Model) updateCompose(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.attachments = nil
		m.state = stateInbox
		return m, nil
	case "ctrl+t":
		return m.launchAttachPickerCmd()
	case "D":
		// Remove last attachment
		if len(m.attachments) > 0 {
			m.attachments = m.attachments[:len(m.attachments)-1]
		}
		return m, nil
	case "ctrl+f":
		froms := m.presendFroms()
		if len(froms) > 1 {
			m.presendFromI = (m.presendFromI + 1) % len(froms)
		}
		return m, nil
	}

	var cmd tea.Cmd
	var launch bool
	m.compose, cmd, launch = m.compose.update(msg)
	if launch {
		return m.launchEditorCmd()
	}
	return m, cmd
}

// updatePresend handles keys in the pre-send review screen.
// a = attach, enter = send, e = re-open editor, D = remove last attachment, esc = cancel
func (m Model) updatePresend(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ps := m.pendingSend
	if ps == nil {
		m.state = stateInbox
		return m, nil
	}
	switch msg.String() {
	case "enter":
		m.loading = true
		m.state = stateInbox
		from := m.presendFrom()
		smtpAcct := m.presendSMTPAccount()
		attachments := m.attachments
		replyUID, replyFolder := ps.replyToUID, ps.replyToFolder
		m.attachments = nil
		m.pendingSend = nil
		return m, tea.Batch(m.spinner.Tick, m.sendEmailCmd(smtpAcct, from, ps.to, ps.cc, ps.bcc, ps.subject, ps.body, attachments, replyUID, replyFolder))
	case "ctrl+f":
		froms := m.presendFroms()
		if len(froms) > 1 {
			m.presendFromI = (m.presendFromI + 1) % len(froms)
		}
		return m, nil
	case "a":
		return m.launchAttachPickerCmd()
	case "D":
		if len(m.attachments) > 0 {
			m.attachments = m.attachments[:len(m.attachments)-1]
		}
		return m, nil
	case "e":
		// Re-open the editor with the current body for further edits.
		m.state = stateCompose
		m.pendingSend = nil
		m.compose.to.SetValue(ps.to)
		m.compose.cc.SetValue(ps.cc)
		m.compose.bcc.SetValue(ps.bcc)
		m.compose.subject.SetValue(ps.subject)
		if ps.cc != "" || ps.bcc != "" {
			m.compose.extraVisible = true
		}
		return m.launchEditorWithBodyCmd(ps.to, ps.cc, ps.bcc, ps.subject, ps.body)
	case "s":
		// Open in nvim with spell checking, cursor on first error.
		return m.launchSpellCheckCmd(ps)
	case "d":
		// Save to Drafts without sending.
		return m, m.saveDraftCmd(m.presendFrom(), ps.to, ps.cc, ps.subject, ps.body, m.attachments)
	case "ctrl+b":
		// Toggle CC/BCC fields — show input prompts to add/edit them.
		m.compose.extraVisible = !m.compose.extraVisible
		if m.compose.extraVisible {
			// Pre-fill from pending data so the user can edit.
			m.compose.cc.SetValue(ps.cc)
			m.compose.bcc.SetValue(ps.bcc)
			m.compose.step = stepCC
			m.compose.cc.Focus()
			m.state = stateCompose
		}
		return m, nil
	case "x":
		// Discard the email entirely — clear everything and go back to inbox.
		m.attachments = nil
		m.pendingSend = nil
		m.state = stateInbox
		m.status = "Discarded."
		m.isError = false
		return m, nil
	case "p":
		return m.previewInBrowser()
	case "esc":
		m.attachments = nil
		m.pendingSend = nil
		m.state = stateInbox
		m.status = "Cancelled."
		return m, nil
	}
	return m, nil
}

// launchSpellCheckCmd opens the composed email body in nvim with spell checking
// enabled and the cursor positioned on the first misspelled word.
// On return, the (possibly corrected) body replaces the pre-send body.
func (m Model) launchSpellCheckCmd(ps *pendingSendData) (tea.Model, tea.Cmd) {
	prelude := editor.Prelude(ps.to, ps.cc, ps.subject, "")
	content := prelude + ps.body

	f, err := os.CreateTemp(neomdTempDir(), "neomd-*.md")
	if err != nil {
		m.status = "spellcheck: " + err.Error()
		m.isError = true
		return m, nil
	}
	tmpPath := f.Name()
	f.WriteString(content) //nolint
	f.Close()

	// Open nvim with spell on and jump to first misspelled word.
	// VimEnter + defer_fn ensures spell activates AFTER all plugins load.
	cmd := exec.Command("nvim",
		"-c", `autocmd VimEnter * ++once lua vim.defer_fn(function() vim.wo.spell = true; vim.bo.spelllang = "en_us,de"; vim.cmd("normal! gg]s") end, 100)`,
		tmpPath,
	)
	m.state = stateCompose
	draftBackups := m.cfg.UI.DraftBackups()
	return m, tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		backupDraft(tmpPath, draftBackups)
		defer os.Remove(tmpPath)
		if execErr != nil {
			return editorDoneMsg{err: execErr}
		}
		raw, readErr := os.ReadFile(tmpPath)
		if readErr != nil {
			return editorDoneMsg{err: readErr}
		}
		pto, pcc, pbcc, psubject, _ := editor.ParseHeaders(string(raw))
		if pto == "" {
			pto = ps.to
		}
		if pcc == "" {
			pcc = ps.cc
		}
		if pbcc == "" {
			pbcc = ps.bcc
		}
		if psubject == "" {
			psubject = ps.subject
		}
		return editorDoneMsg{to: pto, cc: pcc, bcc: pbcc, subject: psubject, body: string(raw)}
	})
}

// previewInBrowser renders the composed email as HTML (same pipeline as sending)
// and opens it in $BROWSER so the user can verify images and formatting.
func (m Model) previewInBrowser() (tea.Model, tea.Cmd) {
	ps := m.pendingSend
	if ps == nil {
		return m, nil
	}

	htmlBody, err := render.ToHTML(ps.body)
	if err != nil {
		m.status = "preview: " + err.Error()
		m.isError = true
		return m, nil
	}

	// Convert absolute image paths to file:// URLs so the browser can display them.
	// goldmark renders ![](/abs/path) as <img src="/abs/path"> which browsers
	// treat as server-relative; file:///abs/path loads from disk.
	htmlBody = strings.ReplaceAll(htmlBody, `src="/`, `src="file:///`)

	f, err := os.CreateTemp(neomdTempDir(), "neomd-preview-*.html")
	if err != nil {
		m.status = "preview: " + err.Error()
		m.isError = true
		return m, nil
	}
	tmpPath := f.Name()
	f.WriteString(htmlBody) //nolint
	f.Close()

	browser := os.Getenv("BROWSER")
	if browser == "" {
		browser = "xdg-open"
	}

	m.status = "Preview opened in browser."
	return m, func() tea.Msg {
		cmd := exec.Command(browser, tmpPath)
		_ = cmd.Start()
		go func() {
			time.Sleep(15 * time.Second)
			os.Remove(tmpPath)
		}()
		return nil
	}
}

func (m Model) saveDraftCmd(from, to, cc, subject, body string, attachments []string) tea.Cmd {
	cli := m.imapCli()
	folder := m.cfg.Folders.Drafts
	return func() tea.Msg {
		raw, err := smtp.BuildMessage(from, to, cc, subject, body, attachments)
		if err != nil {
			return saveDraftDoneMsg{err: err}
		}
		err = cli.SaveDraft(nil, folder, raw)
		return saveDraftDoneMsg{err: err}
	}
}

func (m Model) launchEditorCmd() (tea.Model, tea.Cmd) {
	to := m.compose.to.Value()
	cc := m.compose.cc.Value()
	bcc := m.compose.bcc.Value()
	subject := m.compose.subject.Value()
	prelude := editor.Prelude(to, cc, subject, m.cfg.UI.Signature)

	// Write temp file
	f, err := os.CreateTemp(neomdTempDir(), "neomd-*.md")
	if err != nil {
		m.status = err.Error()
		m.isError = true
		m.state = stateInbox
		return m, nil
	}
	tmpPath := f.Name()
	f.WriteString(prelude) //nolint
	f.Close()

	editorBin := os.Getenv("EDITOR")
	if editorBin == "" {
		editorBin = "nvim"
	}

	cmd := exec.Command(editorBin, tmpPath)
	draftBackups := m.cfg.UI.DraftBackups()
	return m, tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		backupDraft(tmpPath, draftBackups)
		defer os.Remove(tmpPath)
		if execErr != nil {
			return editorDoneMsg{err: execErr}
		}
		raw, readErr := os.ReadFile(tmpPath)
		if readErr != nil {
			return editorDoneMsg{err: readErr}
		}
		if string(raw) == prelude {
			return editorDoneMsg{aborted: true}
		}
		pto, pcc, pbcc, psubject, _ := editor.ParseHeaders(string(raw))
		if pto == "" {
			pto = to
		}
		if pcc == "" {
			pcc = cc
		}
		if pbcc == "" {
			pbcc = bcc
		}
		if psubject == "" {
			psubject = subject
		}
		return editorDoneMsg{to: pto, cc: pcc, bcc: pbcc, subject: psubject, body: string(raw)}
	})
}

// launchEditorWithBodyCmd re-opens the editor with an existing body (e.g. from
// the pre-send screen). The prelude is built from the provided headers (no
// signature — it is already in the body from the first compose).
func (m Model) launchEditorWithBodyCmd(to, cc, bcc, subject, body string) (tea.Model, tea.Cmd) {
	prelude := editor.Prelude(to, cc, subject, "")
	content := prelude + body

	f, err := os.CreateTemp(neomdTempDir(), "neomd-*.md")
	if err != nil {
		m.status = err.Error()
		m.isError = true
		m.state = stateInbox
		return m, nil
	}
	tmpPath := f.Name()
	f.WriteString(content) //nolint
	f.Close()

	editorBin := os.Getenv("EDITOR")
	if editorBin == "" {
		editorBin = "nvim"
	}

	cmd := exec.Command(editorBin, tmpPath)
	draftBackups := m.cfg.UI.DraftBackups()
	return m, tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		backupDraft(tmpPath, draftBackups)
		defer os.Remove(tmpPath)
		if execErr != nil {
			return editorDoneMsg{err: execErr}
		}
		raw, readErr := os.ReadFile(tmpPath)
		if readErr != nil {
			return editorDoneMsg{err: readErr}
		}
		if string(raw) == content {
			return editorDoneMsg{aborted: true}
		}
		pto, pcc, pbcc, psubject, _ := editor.ParseHeaders(string(raw))
		if pto == "" {
			pto = to
		}
		if pcc == "" {
			pcc = cc
		}
		if pbcc == "" {
			pbcc = bcc
		}
		if psubject == "" {
			psubject = subject
		}
		return editorDoneMsg{to: pto, cc: pcc, bcc: pbcc, subject: psubject, body: string(raw)}
	})
}

// launchAttachPickerCmd suspends the TUI, launches yazi (or $NEOMD_FILE_PICKER)
// with --chooser-file, and returns selected paths as attachPickDoneMsg.
// Falls back to a no-op status message if no picker is available.
func (m Model) launchAttachPickerCmd() (tea.Model, tea.Cmd) {
	picker := os.Getenv("NEOMD_FILE_PICKER")
	if picker == "" {
		if _, err := exec.LookPath("yazi"); err == nil {
			picker = "yazi"
		}
	}
	if picker == "" {
		m.status = "No file picker found. Set $NEOMD_FILE_PICKER or install yazi."
		return m, nil
	}

	chooserFile, err := os.CreateTemp(neomdTempDir(), "neomd-pick-*.txt")
	if err != nil {
		m.status = "attach: " + err.Error()
		return m, nil
	}
	chooserPath := chooserFile.Name()
	chooserFile.Close()

	cmd := exec.Command(picker, "--chooser-file", chooserPath)
	return m, tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		defer os.Remove(chooserPath)
		if execErr != nil {
			return attachPickDoneMsg{}
		}
		raw, _ := os.ReadFile(chooserPath)
		var paths []string
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if l := strings.TrimSpace(line); l != "" {
				paths = append(paths, l)
			}
		}
		return attachPickDoneMsg{paths: paths}
	})
}

func (m Model) launchReplyCmd() (tea.Model, tea.Cmd) {
	return m.launchReplyWithCC("", false)
}

func (m Model) launchReplyAllCmd() (tea.Model, tea.Cmd) {
	return m.launchReplyWithCC("", true)
}

func (m Model) launchForwardCmd() (tea.Model, tea.Cmd) {
	e := m.openEmail
	if e == nil {
		return m, nil
	}
	subject := e.Subject
	prelude := editor.ForwardPrelude(subject, m.presendFrom(), e.From, e.Date.Format("Mon, 02 Jan 2006 15:04:05 -0700"), e.To, m.openBody)

	f, err := os.CreateTemp(neomdTempDir(), "neomd-*.md")
	if err != nil {
		m.status = err.Error()
		m.isError = true
		return m, nil
	}
	tmpPath := f.Name()
	f.WriteString(prelude) //nolint
	f.Close()

	editorBin := os.Getenv("EDITOR")
	if editorBin == "" {
		editorBin = "nvim"
	}

	cmd := exec.Command(editorBin, tmpPath)
	draftBackups := m.cfg.UI.DraftBackups()
	return m, tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		backupDraft(tmpPath, draftBackups)
		defer os.Remove(tmpPath)
		if execErr != nil {
			return editorDoneMsg{err: execErr}
		}
		raw, readErr := os.ReadFile(tmpPath)
		if readErr != nil {
			return editorDoneMsg{err: readErr}
		}
		if string(raw) == prelude {
			return editorDoneMsg{aborted: true}
		}
		pto, _, _, psubject, _ := editor.ParseHeaders(string(raw))
		if psubject == "" {
			if !strings.HasPrefix(strings.ToLower(subject), "fwd:") {
				psubject = "Fwd: " + subject
			} else {
				psubject = subject
			}
		}
		return editorDoneMsg{to: pto, cc: "", bcc: "", subject: psubject, body: string(raw)}
	})
}

// launchReplyWithCC is the shared implementation for r (reply) and R (reply-all).
func (m Model) launchReplyWithCC(extraCC string, replyAll bool) (tea.Model, tea.Cmd) {
	e := m.openEmail

	// Auto-select the From address that matches the email's To/CC field.
	// E.g. if email was sent to simon@ssp.sh, reply from simon@ssp.sh.
	if idx := m.matchFromIndex(e.To, e.CC); idx >= 0 {
		m.presendFromI = idx
	}

	// Use Reply-To if present, else From
	to := e.ReplyTo
	if to == "" {
		to = e.From
	}

	subject := e.Subject
	low := strings.ToLower(subject)
	hasReplyPrefix := strings.HasPrefix(low, "re:") ||
		strings.HasPrefix(low, "aw:") ||
		strings.HasPrefix(low, "sv:") ||
		strings.HasPrefix(low, "vs:")
	if !hasReplyPrefix {
		subject = "Re: " + subject
	}

	cc := ""
	if replyAll {
		// Collect original To + CC, exclude own address
		own := strings.ToLower(extractEmailAddr(m.activeAccount().User))
		var parts []string
		for _, addr := range splitAddrs(e.To + "," + e.CC) {
			if a := strings.TrimSpace(addr); a != "" && strings.ToLower(extractEmailAddr(a)) != own {
				parts = append(parts, a)
			}
		}
		cc = strings.Join(parts, ", ")
	}
	if extraCC != "" {
		if cc != "" {
			cc += ", " + extraCC
		} else {
			cc = extraCC
		}
	}

	prelude := editor.ReplyPrelude(to, cc, subject, m.presendFrom(), e.From, m.openBody)

	f, err := os.CreateTemp(neomdTempDir(), "neomd-*.md")
	if err != nil {
		m.status = err.Error()
		m.isError = true
		return m, nil
	}
	tmpPath := f.Name()
	f.WriteString(prelude) //nolint
	f.Close()

	editorBin := os.Getenv("EDITOR")
	if editorBin == "" {
		editorBin = "nvim"
	}

	cmd := exec.Command(editorBin, tmpPath)
	draftBackups := m.cfg.UI.DraftBackups()
	return m, tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		backupDraft(tmpPath, draftBackups)
		defer os.Remove(tmpPath)
		if execErr != nil {
			return editorDoneMsg{err: execErr}
		}
		raw, readErr := os.ReadFile(tmpPath)
		if readErr != nil {
			return editorDoneMsg{err: readErr}
		}
		if string(raw) == prelude {
			return editorDoneMsg{aborted: true}
		}
		pto, pcc, _, psubject, _ := editor.ParseHeaders(string(raw))
		if pto == "" {
			pto = to
		}
		if pcc == "" {
			pcc = cc
		}
		if psubject == "" {
			psubject = subject
		}
		return editorDoneMsg{to: pto, cc: pcc, bcc: "", subject: psubject, body: string(raw)}
	})
}

// matchFromIndex returns the presendFroms() index whose email address matches
// one of the addresses in the email's To or CC fields. Returns -1 if no match.
// This auto-selects the correct From when replying to an email sent to an alias.
func (m Model) matchFromIndex(toField, ccField string) int {
	froms := m.presendFroms()
	recipients := make(map[string]bool)
	for _, addr := range splitAddrs(toField + "," + ccField) {
		if a := strings.ToLower(extractEmailAddr(addr)); a != "" {
			recipients[a] = true
		}
	}
	for i, from := range froms {
		if recipients[strings.ToLower(extractEmailAddr(from))] {
			return i
		}
	}
	return -1
}

// extractEmailAddr returns the bare email address from "Name <addr>" or "addr".
// mergeAutoBCC appends autoBCC to the existing bcc field, deduped by email
// address. Returns bcc unchanged when autoBCC is empty or already present.
func mergeAutoBCC(bcc, autoBCC string) string {
	autoBCC = strings.TrimSpace(autoBCC)
	if autoBCC == "" {
		return bcc
	}
	autoAddr := strings.ToLower(extractEmailAddr(autoBCC))
	for _, p := range strings.Split(bcc, ",") {
		if strings.ToLower(extractEmailAddr(strings.TrimSpace(p))) == autoAddr {
			return bcc
		}
	}
	if strings.TrimSpace(bcc) == "" {
		return autoBCC
	}
	return bcc + ", " + autoBCC
}

func extractEmailAddr(s string) string {
	if i := strings.IndexByte(s, '<'); i >= 0 {
		if j := strings.IndexByte(s, '>'); j > i {
			return strings.TrimSpace(s[i+1 : j])
		}
	}
	return strings.TrimSpace(s)
}

// splitAddrs splits a comma-separated address list, skipping empty entries.
func splitAddrs(s string) []string {
	var out []string
	for _, a := range strings.Split(s, ",") {
		if t := strings.TrimSpace(a); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// imageExts are file extensions treated as inline images (embedded via CID in HTML).
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".webp": true, ".svg": true,
}

// stripPrelude removes the # [neomd: to/cc/bcc/subject: ...] header lines that
// Prelude() and ReplyPrelude() prepend to compose temp files as editor hints.
// These must not appear in sent mail — they're stripped here before the body
// reaches smtp.BuildMessage.
func stripPrelude(body string) string {
	lines := strings.Split(body, "\n")
	var kept []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "# [neomd: to:") ||
			strings.HasPrefix(t, "# [neomd: cc:") ||
			strings.HasPrefix(t, "# [neomd: bcc:") ||
			strings.HasPrefix(t, "# [neomd: from:") ||
			strings.HasPrefix(t, "# [neomd: subject:") {
			continue
		}
		kept = append(kept, line)
	}
	// Drop leading blank lines left after removing the header block.
	for len(kept) > 0 && strings.TrimSpace(kept[0]) == "" {
		kept = kept[1:]
	}
	return strings.Join(kept, "\n")
}

// extractInlineAttachments scans body for [attach] /path lines inserted by the
// neomd Lua helper in neovim (<leader>a).
//   - Image files (.png, .jpg, …) are converted to ![](path) markdown refs so
//     goldmark renders them as <img> tags inline; the sender embeds them via CID.
//   - Non-image files are returned as file attachment paths (appended at bottom).
//
// Returns (filePaths, cleanBody).
func extractInlineAttachments(body string) (files []string, clean string) {
	const prefix = "[attach] "
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			p := strings.TrimSpace(trimmed[len(prefix):])
			if p == "" {
				continue
			}
			if imageExts[strings.ToLower(filepath.Ext(p))] {
				// Inline: replace with markdown image ref
				kept = append(kept, "![]("+p+")")
			} else {
				files = append(files, p)
			}
			continue
		}
		kept = append(kept, line)
	}
	return files, strings.Join(kept, "\n")
}

// ── View ──────────────────────────────────────────────────────────────────

func (m Model) View() string {
	if m.width == 0 {
		return "Loading…"
	}
	switch m.state {
	case stateInbox:
		return m.viewInbox()
	case stateReading:
		return m.viewReader()
	case stateCompose:
		return m.viewCompose()
	case statePresend:
		return m.viewPresend()
	case stateHelp:
		return m.viewHelp()
	case stateWelcome:
		return m.viewWelcome()
	}
	return ""
}

func (m Model) viewPresend() string {
	ps := m.pendingSend
	if ps == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(styleHeader.Render("  Ready to send") + "\n")
	b.WriteString(styleSeparator.Render(strings.Repeat("─", m.width)) + "\n\n")

	lbl := styleInputLabel.Render
	fromLine := m.presendFrom()
	if len(m.presendFroms()) > 1 {
		fromLine += styleHelp.Render("  (ctrl+f to cycle)")
	}
	b.WriteString(lbl("From:") + " " + fromLine + "\n")
	b.WriteString(lbl("To:") + "  " + ps.to + "\n")
	if ps.cc != "" {
		b.WriteString(lbl("Cc:") + "  " + ps.cc + "\n")
	}
	if ps.bcc != "" {
		b.WriteString(lbl("Bcc:") + " " + ps.bcc + "\n")
	}
	b.WriteString(lbl("Subject:") + " " + ps.subject + "\n")

	// Show first 3 non-empty lines of body as a preview (skip [attach] lines already extracted)
	preview := 0
	for _, line := range strings.Split(ps.body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		b.WriteString(styleHelp.Render("  > "+line) + "\n")
		preview++
		if preview >= 3 {
			break
		}
	}

	b.WriteString("\n")
	if len(m.attachments) > 0 {
		for _, a := range m.attachments {
			b.WriteString("  [attach] " + filepath.Base(a) + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(styleHelp.Render("  enter send · s spell · p preview · a attach · D remove attach · ctrl+f from · ctrl+b cc/bcc · e edit · d draft · esc cancel · x discard"))
	return b.String()
}

func (m Model) viewInbox() string {
	var b strings.Builder

	// Account indicator (only shown when more than one account configured)
	activeTab := m.folders[m.activeFolderI]
	if m.offTabFolder != "" {
		activeTab = "" // deselect all tabs; off-tab folder shown separately
	}
	header, _ := folderTabs(m.folders, activeTab, m.folderCounts)
	if m.offTabFolder != "" {
		header += styleSeparator.Render(" │ ") + styleHeader.Render(m.offTabFolder)
	}
	if len(m.accounts) > 1 {
		acct := styleDate.Render("  " + m.activeAccount().Name + " ·")
		header = acct + "  " + header
	}
	if len(m.markedUIDs) > 0 {
		header += styleDate.Render(fmt.Sprintf("  [%d marked · U to clear]", len(m.markedUIDs)))
	}
	b.WriteString(header + "\n")
	b.WriteString(styleSeparator.Render(strings.Repeat("─", m.width)) + "\n")

	if m.loading {
		loadingText := "Loading…"
		if bp := m.bulkProgress; bp != nil && bp.total > 0 {
			loadingText = bp.String()
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", m.spinner.View(), loadingText))
	} else if len(m.emails) == 0 {
		b.WriteString(styleStatus.Render("  No messages.") + "\n")
	} else {
		b.WriteString(m.inbox.View())
	}

	b.WriteString("\n")
	if m.cmdMode {
		b.WriteString(viewCmdLine(m.cmdText, m.width))
	} else if m.imapSearchActive || m.imapSearchResults {
		b.WriteString(m.viewIMAPSearchBar())
	} else if m.filterActive || m.filterText != "" {
		cursor := ""
		if m.filterActive {
			cursor = "█"
		}
		b.WriteString(styleHelp.Render(fmt.Sprintf("  / %s%s  · enter confirm · esc clear", m.filterText, cursor)))
	} else if m.status != "" {
		b.WriteString(statusBar(m.status, m.isError))
	} else {
		help := inboxHelp(m.folders[m.activeFolderI])
		if len(m.accounts) > 1 {
			help += styleHelp.Render(" · ctrl+a switch account")
		}
		b.WriteString(help)
	}
	return b.String()
}

func (m Model) viewReader() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("  ← q") + "  " + styleStatus.Render(m.folders[m.activeFolderI]) + "\n")
	if m.loading {
		b.WriteString(fmt.Sprintf("  %s Loading…\n", m.spinner.View()))
	} else {
		b.WriteString(m.reader.View())
	}
	isDraft := m.openEmail != nil && m.openEmail.Folder == m.cfg.Folders.Drafts
	b.WriteString("\n" + readerHelp(isDraft, len(m.openLinks) > 0))
	return b.String()
}

func (m Model) viewCompose() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("  New Message") + "\n")
	b.WriteString(styleSeparator.Render(strings.Repeat("─", m.width)) + "\n\n")

	// From line — always shown so you know who you're sending as.
	lbl := styleInputLabel.Render
	fromLine := m.presendFrom()
	if len(m.presendFroms()) > 1 {
		fromLine += styleHelp.Render("  (ctrl+f to cycle)")
	}
	b.WriteString(lbl("From:") + " " + fromLine + "\n")

	b.WriteString(m.compose.view() + "\n\n")
	if len(m.attachments) > 0 {
		for _, a := range m.attachments {
			b.WriteString("  [attach] " + filepath.Base(a) + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(composeHelp(int(m.compose.step), len(m.presendFroms()) > 1))
	return b.String()
}

func (m Model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.helpSearch != "" {
			m.helpSearch = "" // first esc clears filter
		} else {
			m.state = m.prevState
		}
	case "q":
		if m.helpSearch == "" {
			m.state = m.prevState
		} else {
			m.helpSearch += "q"
		}
	case "backspace":
		if len(m.helpSearch) > 0 {
			m.helpSearch = m.helpSearch[:len([]rune(m.helpSearch))-1]
		}
	case "/":
		// already in search mode — "/" is just a printable char if search active
		if m.helpSearch == "" {
			// start typing to search; "/" itself doesn't appear
		} else {
			m.helpSearch += "/"
		}
	default:
		// printable single character: append to search
		if len(msg.String()) == 1 {
			m.helpSearch += msg.String()
		}
	}
	return m, nil
}

func (m Model) viewWelcome() string {
	boxWidth := 64
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPrimary).
		Padding(1, 3).
		Width(boxWidth)

	title := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	key := lipgloss.NewStyle().Foreground(colorAuthorUnread).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colorDateCol)

	warn := lipgloss.NewStyle().Foreground(colorError)

	content := title.Render("Welcome to neomd!") + "\n\n" +
		"Your IMAP folders have been created automatically.\n\n" +
		title.Render("Quick start") + "\n" +
		key.Render("  j/k") + "  navigate    " + key.Render("enter") + "  open email\n" +
		key.Render("  c") + "    compose      " + key.Render("r") + "      reply\n" +
		key.Render("  f") + "    forward      " + key.Render("R") + "      reply-all\n" +
		key.Render("  ]") + " / " + key.Render("[") + "  next/prev tab  " + key.Render("?") + "  all keys\n\n" +
		title.Render("How the Screener works") + "\n" +
		"Your screener lists are empty, so " + warn.Render("auto-screening") + "\n" +
		warn.Render("is paused") + " until you classify your first senders.\n\n" +
		title.Render("Getting started") + "\n" +
		"1. Go to " + key.Render("ToScreen") + " tab (" + key.Render("gk") + " or " + key.Render("Tab") + " or click)\n" +
		"2. Screen each sender:\n" +
		key.Render("   I") + "  screen " + title.Render("in") + "   " + dim.Render("sender stays in Inbox forever") + "\n" +
		key.Render("   O") + "  screen " + title.Render("out") + "  " + dim.Render("sender never reaches Inbox again") + "\n" +
		key.Render("   F") + "  feed        " + dim.Render("newsletters go to Feed tab") + "\n" +
		key.Render("   P") + "  papertrail  " + dim.Render("receipts go to PaperTrail tab") + "\n" +
		"3. Use " + key.Render("m") + " to mark multiple, then " + key.Render("I") + " to batch-approve\n\n" +
		dim.Render("Once classified, senders are remembered forever.") + "\n" +
		dim.Render("New emails auto-sort on every load. You choose") + "\n" +
		dim.Render("who lands in your inbox. Bye-bye spam.") + "\n\n" +
		dim.Render("Disable auto-screen: auto_screen_on_load = false") + "\n" +
		dim.Render("Diagnostics: :debug   All keys: ?") + "\n\n" +
		dim.Render("Press any key to continue.")

	rendered := box.Render(content)

	// Center vertically and horizontally
	lines := strings.Count(rendered, "\n") + 1
	padTop := (m.height - lines) / 2
	if padTop < 0 {
		padTop = 0
	}
	padLeft := (m.width - boxWidth) / 2
	if padLeft < 0 {
		padLeft = 0
	}
	prefix := strings.Repeat(" ", padLeft)
	var b strings.Builder
	for i := 0; i < padTop; i++ {
		b.WriteByte('\n')
	}
	for _, line := range strings.Split(rendered, "\n") {
		b.WriteString(prefix + line + "\n")
	}
	return b.String()
}

func (m Model) viewHelp() string {
	heading := styleHeader.Render("  Keyboard shortcuts")
	sep := styleSeparator.Render(strings.Repeat("─", m.width))

	keyStyle := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true).Width(24)
	titleStyle := lipgloss.NewStyle().Foreground(colorDateCol).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(colorText)
	matchStyle := lipgloss.NewStyle().Foreground(colorAuthorUnread).Bold(true)

	filter := strings.ToLower(m.helpSearch)

	var b strings.Builder
	b.WriteString(heading + "\n" + sep + "\n")
	for _, sec := range HelpSections {
		var matched [][2]string
		for _, row := range sec.Rows {
			if filter == "" || strings.Contains(strings.ToLower(row[0]), filter) || strings.Contains(strings.ToLower(row[1]), filter) {
				matched = append(matched, row)
			}
		}
		if len(matched) == 0 {
			continue
		}
		b.WriteString("\n" + titleStyle.Render("  "+sec.Title) + "\n")
		for _, row := range matched {
			b.WriteString("  " + keyStyle.Render(row[0]) + descStyle.Render(row[1]) + "\n")
		}
	}

	// Search bar
	var searchLine string
	if filter != "" {
		searchLine = matchStyle.Render("  /"+m.helpSearch) + styleHelp.Render("  · esc to clear")
	} else {
		searchLine = styleHelp.Render("  type to filter · ? or q to close")
	}
	b.WriteString("\n" + searchLine)
	return b.String()
}

// ── Helpers ───────────────────────────────────────────────────────────────

func splitAddr(addr string) (host, port string) {
	h, p, _ := strings.Cut(addr, ":")
	if p == "" {
		p = "587"
	}
	return h, p
}

// Ensure Model satisfies tea.Model.
var _ tea.Model = Model{}
