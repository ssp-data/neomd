// Package ui contains the bubbletea TUI model for neomd.
package ui

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sspaeti/neomd/internal/calendar"
	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/editor"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/listmonk"
	"github.com/sspaeti/neomd/internal/notify"
	"github.com/sspaeti/neomd/internal/render"
	"github.com/sspaeti/neomd/internal/screener"
	"github.com/sspaeti/neomd/internal/smtp"
)

// viewState is the current screen.
type viewState int

const (
	stateInbox    viewState = iota
	stateReading            // reading a single email
	stateCompose            // composing a new email
	statePresend            // pre-send review: add attachments, then send or edit again
	stateHelp               // help overlay
	stateWelcome            // first-run welcome popup
	stateReaction           // emoji reaction picker
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
		references  string // References header for email threading
		spyPixels   imap.SpyPixelInfo
	}
	sendDoneMsg struct {
		err           error
		warning       string
		info          string // non-error informational message (e.g. Listmonk success)
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
	// spyScanProgressMsg reports results of :scan-spy-pixels.
	spyScanProgressMsg struct {
		spyKeys     []string // keys where spy pixels were found
		scannedKeys []string // all keys that were scanned (for cache)
		scanned     int
		total       int
		found       int
		done        bool
		err         error
	}
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

	// bgVipFolderFetchedMsg carries a non-Inbox folder fetch used purely to
	// dispatch desktop notifications for VIP senders whose mail the daemon
	// (or any other sync path) may have already moved out of Inbox.
	bgVipFolderFetchedMsg struct {
		folder string
		emails []imap.Email
	}
	// mark-as-read timer (fires after N seconds in reader)
	markAsReadTimerMsg struct {
		uid    uint32
		folder string
	}
	// attachPickDoneMsg carries paths selected via the file picker (yazi etc.)
	attachPickDoneMsg struct{ paths []string }
	// bulkProgressMsg is sent during long-running batch operations to update the status bar.
	bulkProgressMsg struct {
		moved, total int
		label        string
	}
	saveDraftDoneMsg  struct{ err error }
	attachOpenDoneMsg struct {
		path      string
		err       error
		dangerous bool   // true if file was saved but NOT auto-opened
		reason    string // why it was flagged (shown in status bar)
	}
	emlDownloadedMsg struct {
		path string
		err  error
	}
	editorDoneMsg struct {
		to, cc, bcc, from, subject, body string
		err                              error
		aborted                          bool // true when file was unchanged (ZQ / :q!)
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

// MailtoParams holds pre-filled compose fields from a mailto: URI.
// When non-nil, the TUI starts directly in compose mode.
type MailtoParams struct {
	To      string
	CC      string
	BCC     string
	Subject string
	Body    string
}

// neomdTempDir returns /tmp/neomd/, creating it if needed.
// Using a dedicated subdirectory keeps temp files discoverable (e.g. recovering
// a draft after a crash) and avoids cluttering /tmp/.
func neomdTempDir() string {
	dir := filepath.Join(os.TempDir(), "neomd")
	os.MkdirAll(dir, 0700) //nolint
	return dir
}

func detectStartupNotice() string {
	_, hasYazi := exec.LookPath("yazi")
	home, _ := os.UserHomeDir()
	customLua := filepath.Join(home, ".config", "nvim", "lua", "sspaeti", "custom.lua")
	_, customLuaErr := os.Stat(customLua)

	switch {
	case hasYazi != nil && customLuaErr != nil:
		return "Optional inline <leader>a attachments in nvim are unavailable: install yazi and add the custom.lua integration. Pre-send 'a' still works."
	case hasYazi != nil:
		return "Optional inline <leader>a attachments in nvim are unavailable: install yazi. Pre-send 'a' still works."
	case customLuaErr != nil:
		return "Optional inline <leader>a attachments in nvim are not configured. Add the custom.lua integration if you want that workflow; pre-send 'a' still works."
	default:
		return ""
	}
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
			if a.IMAPDisabled {
				active += " (imap disabled)"
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
			if cli == nil {
				// imap_disabled accounts have a nil client by design (send-only).
				b.WriteString(fmt.Sprintf("- **%s** — IMAP disabled (send-only)\n", name))
				continue
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
			{"notify", sc.Notify},
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

		// Notifications
		b.WriteString("\n## Notifications\n\n")
		nc := m.cfg.Notifications.Resolved()
		b.WriteString(fmt.Sprintf("- enabled: %v\n", nc.Enabled))
		b.WriteString(fmt.Sprintf("- command: `%s`\n", nc.Command))
		b.WriteString(fmt.Sprintf("- icon: `%s`\n", nc.Icon))
		b.WriteString(fmt.Sprintf("- expire_ms: %d\n", nc.ExpireMs))
		b.WriteString(fmt.Sprintf("- folders (allowlist): %s\n", strings.Join(nc.Folders, ", ")))
		statePath := config.NotifyStatePath()
		if data, err := os.ReadFile(statePath); err == nil {
			b.WriteString(fmt.Sprintf("- state file: `%s` → `%s`\n", statePath, strings.TrimSpace(string(data))))
		} else {
			b.WriteString(fmt.Sprintf("- state file: `%s` (not yet written)\n", statePath))
		}
		b.WriteString(fmt.Sprintf("- bg-poll folders (non-Inbox): %s\n", strings.Join(m.vipNotifyFolders(), ", ")))

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
	replyToUID     uint32
	replyToFolder  string
	replyToAccount string
	// Threading headers for proper email conversation threading
	inReplyTo  string
	references string
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

// pendingDomainAction queues a domain-level screener mutation awaiting y/n.
// entry is the storage form ("@ssp.sh"); action is "I" (approve) or "O" (block).
type pendingDomainAction struct {
	entry  string
	action string
}

// Model is the root bubbletea model.
type Model struct {
	cfg         *config.Config
	accounts    []config.AccountConfig // all configured accounts
	clients     []*imap.Client         // one IMAP client per account
	accountI    int                    // index of the active account
	screener    *screener.Screener
	notifier    *notify.Notifier
	notifyState *notify.State

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
	openSpyPixels   imap.SpyPixelInfo // spy pixels detected in the currently open email
	readerPending   string            // chord prefix in reader (space for link open)
	// Mark-as-read timer tracking
	markAsReadUID    uint32 // UID of email with pending mark-as-read timer
	markAsReadFolder string // folder of email with pending mark-as-read timer

	// Compose / pre-send
	compose      composeModel
	attachments  []string // files to attach to the next send (cleared after send)
	pendingSend  *pendingSendData
	presendFromI int // index into presendFroms() for the From field cycle

	// AI handoff (pre-send `i`) — when active, shows a one-line input for the
	// instruction that gets substituted into [ai].args via {prompt}. Empty
	// input falls through to interactive mode (no substitution).
	aiPromptActive bool
	aiPromptInput  textinput.Model

	// Reaction
	reactionEmail    *imap.Email // email being reacted to
	reactionSelected int         // selected emoji index (0-7)
	pendingReaction  bool        // true if we need to fetch body before entering reaction mode

	// Status / error
	status        string
	isError       bool
	startupNotice string

	// Auto-screen dry-run: populated by S, cleared by y/n
	pendingMoves []autoScreenMove

	// Marked emails for batch operations (UID → true)
	markedUIDs map[uint32]bool

	// Spy pixel tracking: "folder\x00uid" → true when email body contained tracking pixels.
	// Populated on body load, used to show ⊙ indicator in inbox list.
	// Keyed by folder+UID to avoid collisions across mailboxes (UIDs are only unique per folder).
	spyPixelKeys map[string]bool
	// spyScannedKeys tracks which emails have been scanned (positive or negative).
	// Used to skip already-scanned emails in :scan-spy-pixels.
	spyScannedKeys map[string]bool

	// Undo stack: each entry is a batch of moves that can be reversed with u.
	// Screener operations (I/O/F/P/$) are not undoable — they also modify .txt files.
	undoStack [][]undoMove

	// Forward/Reply: when true, bodyLoadedMsg launches the action instead of reader
	pendingForward  bool
	pendingReply    bool
	pendingReplyAll bool

	// bgSyncInProgress prevents concurrent background syncs from piling up
	bgSyncInProgress bool

	// Chord prefix: "g" or "M" while waiting for second key
	pendingKey string

	// prevState is the state to return to when closing the help overlay
	prevState viewState

	// helpSearch / helpScroll track the ? overlay state.
	helpSearch       string
	helpSearchActive bool
	helpScroll       int

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

	// showUnreadOnly filters the inbox to show only unread emails when true.
	// Toggled with 'v' key.
	showUnreadOnly bool

	// pendingResetUIDs holds ToScreen UIDs awaiting y/n confirmation before
	// being bulk-moved back to Inbox.
	pendingResetUIDs []uint32

	// pendingDomainOp holds an "@domain" screener entry (e.g. "@ssp.sh")
	// awaiting y/n confirmation, plus the action to execute ("I" approve,
	// "O" block). Set by the Di / Do reader chord; cleared on y, n, or any
	// other key.
	pendingDomainOp *pendingDomainAction

	// pendingDeleteAll holds UIDs + folder awaiting y/n before permanent deletion.
	pendingDeleteAll *deleteAllReadyMsg

	// pendingDiscard asks for y/n confirmation before dropping unsent compose state.
	pendingDiscard bool

	// folderCounts holds unseen message counts for watched folder tabs.
	// Keys are tab labels: "Inbox", "PaperTrail", "Waiting", "Scheduled".
	folderCounts map[string]int

	// Sort state. sortField is one of "date", "from", "subject", "size".
	// sortReverse=true means newest/largest/Z-first (descending).
	// Default: date descending (newest first).
	sortField   string
	sortReverse bool

	// mailto holds pre-filled compose fields from a mailto: URI.
	// When set, the TUI opens compose immediately after init.
	mailto     *MailtoParams
	mailtoBody string // body from mailto URI, consumed by launchEditorCmd
}

// New creates and initialises the TUI model.
func New(cfg *config.Config, clients []*imap.Client, sc *screener.Screener, mailto ...*MailtoParams) Model {
	// Theme is applied here so any in-memory rendering inside this constructor
	// already uses the user's chosen palette. Default name is "kanagawa" for
	// byte-for-byte parity with the pre-theme state when [ui].theme is unset.
	themeName := cfg.UI.Theme
	if themeName == "" || themeName == "dark" || themeName == "light" || themeName == "auto" {
		// Legacy [ui].theme values (pre-named-theme era) → kanagawa default.
		themeName = "kanagawa"
	}
	ApplyTheme(themeName, cfg.Theme)

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	compose := newComposeModel()
	compose.knownAddrs = sc.AllAddresses()

	var mp *MailtoParams
	if len(mailto) > 0 {
		mp = mailto[0]
	}

	spyKeys, scannedKeys := loadSpyPixelCache()
	return Model{
		cfg:         cfg,
		accounts:    cfg.ActiveAccounts(),
		clients:     clients,
		screener:    sc,
		notifier:    notify.New(cfg.Notifications),
		notifyState: notify.LoadState(config.NotifyStatePath()),
		state:       stateInbox,
		loading:     true,
		folders:     cfg.Folders.TabLabels(),
		cmdHistory:  loadCmdHistory(config.HistoryPath()),
		cmdHistI:    -1,
		// Note: Spam is intentionally excluded from tabs — use :go-spam to visit.
		compose:        compose,
		spinner:        sp,
		markedUIDs:     make(map[uint32]bool),
		spyPixelKeys:   spyKeys,
		spyScannedKeys: scannedKeys,
		startupNotice:  detectStartupNotice(),
		sortField:      "date",
		sortReverse:    true, // newest first
		mailto:         mp,
	}
}

// tokenSourceFor returns the OAuth2 token source for the account with the
// given name, or nil if the account uses plain password authentication or
// is configured as send-only (imap_disabled — its client is nil because
// neomd never opened an IMAP connection).
func (m Model) tokenSourceFor(accountName string) func() (string, error) {
	for i, acc := range m.accounts {
		if acc.Name == accountName && i < len(m.clients) && m.clients[i] != nil {
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

func (m Model) imapCliForAccount(accountName string) *imap.Client {
	for i, a := range m.accounts {
		if strings.EqualFold(a.Name, accountName) && i < len(m.clients) && m.clients[i] != nil {
			return m.clients[i]
		}
	}
	// Account not found, or its client is nil because imap_disabled — fall
	// back to whatever imapCli / primaryIMAPClient resolves to.
	return m.imapCli()
}

func (m Model) primaryIMAPClient() *imap.Client {
	// Pick the first non-nil client — accounts with imap_disabled = true
	// have a nil entry by design, so we cannot blindly use index 0.
	for _, c := range m.clients {
		if c != nil {
			return c
		}
	}
	return nil
}

func (m Model) sentDraftsIMAPClient() *imap.Client {
	if m.cfg != nil && m.cfg.StoreSentDraftsInSendingAccount {
		return m.imapCliForAccount(m.presendSMTPAccount().Name)
	}
	return m.primaryIMAPClient()
}

func (m Model) presendIMAPClient() *imap.Client {
	return m.sentDraftsIMAPClient()
}

func (m *Model) applyEditedFrom(from string) {
	if idx := m.matchFromAddress(from); idx >= 0 {
		m.presendFromI = idx
	}
}

// imapCli returns the IMAP client for the active account, falling back to
// the first non-nil client if the active account is imap_disabled.
func (m Model) imapCli() *imap.Client {
	if m.accountI < len(m.clients) && m.clients[m.accountI] != nil {
		return m.clients[m.accountI]
	}
	return m.primaryIMAPClient()
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
	switch m.offTabFolder {
	case "Drafts":
		return m.cfg.Folders.Drafts
	case "Spam":
		return m.cfg.Folders.Spam
	}
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
		body, rawHTML, webURL, attachments, references, spyPixels, err := m.imapCli().FetchBody(nil, e.Folder, e.UID)
		if err != nil {
			return errMsg{err}
		}
		return bodyLoadedMsg{email: e, body: body, rawHTML: rawHTML, webURL: webURL, attachments: attachments, references: references, spyPixels: spyPixels}
	}
}

func (m Model) sendEmailCmd(smtpAcct config.AccountConfig, from, to, cc, bcc, subject, body string, attachments []string, includeHTMLSig bool, replyToUID uint32, replyToFolder, replyToAccount, inReplyTo, references string) tea.Cmd {
	h, p := splitAddr(smtpAcct.SMTP)
	cfg := smtp.Config{
		Host:        h,
		Port:        p,
		User:        smtpAcct.User,
		Password:    smtpAcct.Password,
		From:        from,
		STARTTLS:    smtpAcct.STARTTLS,
		TLSCertFile: smtpAcct.TLSCertFile,
		TokenSource: m.tokenSourceFor(smtpAcct.Name),
	}
	cli := m.sentDraftsIMAPClient()
	sentFolder := m.cfg.Folders.Sent
	replyCli := m.imapCliForAccount(replyToAccount)
	htmlSignature := ""
	if includeHTMLSig {
		htmlSignature = m.cfg.UI.HTMLSignature()
	}
	return func() tea.Msg {
		// Build raw MIME once — reused for both SMTP delivery and Sent copy.
		// BCC is intentionally excluded from headers but included in RCPT TO.
		raw, err := smtp.BuildMessageWithThreading(from, to, cc, subject, body, attachments, htmlSignature, inReplyTo, references)
		if err != nil {
			return sendDoneMsg{err: fmt.Errorf("build message: %w", err)}
		}
		toAddrs := collectRcptTo(to, cc, bcc)
		if err := smtp.SendRaw(cfg, toAddrs, raw); err != nil {
			return sendDoneMsg{err: err}
		}
		// Save copy to Sent; non-fatal if it fails, but warn user.
		// cli can be nil when every configured account has imap_disabled = true
		// (send-only setups) — in that case there is no IMAP target for the
		// Sent copy, so we skip silently.
		if cli != nil {
			if saveErr := cli.SaveSent(nil, sentFolder, raw); saveErr != nil {
				return sendDoneMsg{warning: "Sent, but failed to save to Sent folder: " + saveErr.Error(), replyToUID: replyToUID, replyToFolder: replyToFolder}
			}
		}
		// Mark original email as \Answered (non-fatal).
		if replyToUID > 0 && replyToFolder != "" && replyCli != nil {
			_ = replyCli.MarkAnswered(nil, replyToFolder, replyToUID)
		}
		return sendDoneMsg{replyToUID: replyToUID, replyToFolder: replyToFolder}
	}
}

// listmonkTriggers converts config triggers to listmonk.Trigger slice.
func (m Model) listmonkTriggers() []listmonk.Trigger {
	triggers := make([]listmonk.Trigger, len(m.cfg.Listmonk.Triggers))
	for i, t := range m.cfg.Listmonk.Triggers {
		triggers[i] = listmonk.Trigger{Address: t.Address, ListIDs: t.ListIDs}
	}
	return triggers
}

func (m Model) sendListmonkCmd(subject, markdownBody string, listIDs []int) tea.Cmd {
	cfg := m.cfg.Listmonk
	delay := time.Duration(cfg.DelayMinutes) * time.Minute
	if delay == 0 {
		delay = 30 * time.Minute
	}
	return func() tea.Msg {
		client := listmonk.NewClient(listmonk.Config{
			URL:      cfg.URL,
			APIUser:  cfg.APIUser,
			APIToken: cfg.APIToken,
		})
		campaignID, err := client.CreateAndSchedule(subject, markdownBody, listIDs, delay)
		if err != nil {
			return sendDoneMsg{err: fmt.Errorf("listmonk: %w", err)}
		}
		mins := cfg.DelayMinutes
		if mins == 0 {
			mins = 30
		}
		return sendDoneMsg{info: fmt.Sprintf("Campaign #%d scheduled via Listmonk (sends in %d min)", campaignID, mins)}
	}
}

func (m Model) sendReaction(emojiIndex int) (tea.Model, tea.Cmd) {
	if m.reactionEmail == nil || emojiIndex < 0 || emojiIndex >= len(defaultReactions) {
		return m, nil
	}

	emoji := defaultReactions[emojiIndex]
	e := m.reactionEmail

	// Determine recipient (Reply-To takes precedence over From)
	to := e.ReplyTo
	if to == "" {
		to = e.From
	}

	// Build subject with "Re:" prefix
	subject := e.Subject
	low := strings.ToLower(subject)
	if !strings.HasPrefix(low, "re:") && !strings.HasPrefix(low, "aw:") &&
		!strings.HasPrefix(low, "sv:") && !strings.HasPrefix(low, "vs:") {
		subject = "Re: " + subject
	}

	// Extract sender name for footer
	from := m.presendFrom()
	fromName := extractName(from)
	if fromName == "" {
		fromName = extractEmailAddr(from)
	}

	// Build reaction body in markdown (used for both text/plain and text/html parts, same as regular replies)
	bodyMarkdown := editor.ReactionBody(emoji.emoji, fromName, e.From, m.openBody)

	// Get SMTP account matching the selected From. presendFromI is pre-set by
	// matchFromIndex in enterReactionMode based on which of our addresses
	// received the email, so this resolves to the correct account/alias SMTP.
	smtpAcct := m.presendSMTPAccount()

	// Reset state before sending
	m.state = m.prevState
	m.loading = true
	m.status = fmt.Sprintf("Sending %s...", emoji.emoji)
	m.reactionEmail = nil

	return m, tea.Batch(
		m.spinner.Tick,
		m.sendReactionCmd(smtpAcct, from, to, subject, bodyMarkdown, e),
	)
}

func (m Model) sendReactionCmd(smtpAcct config.AccountConfig, from, to, subject, bodyMarkdown string, originalEmail *imap.Email) tea.Cmd {
	h, p := splitAddr(smtpAcct.SMTP)
	cfg := smtp.Config{
		Host:        h,
		Port:        p,
		User:        smtpAcct.User,
		Password:    smtpAcct.Password,
		From:        from,
		STARTTLS:    smtpAcct.STARTTLS,
		TLSCertFile: smtpAcct.TLSCertFile,
		TokenSource: m.tokenSourceFor(smtpAcct.Name),
	}
	cli := m.sentDraftsIMAPClient()
	sentFolder := m.cfg.Folders.Sent
	replyCli := m.imapCli()

	return func() tea.Msg {
		// Build References chain: use existing References or fall back to InReplyTo
		references := originalEmail.References
		if references == "" && originalEmail.InReplyTo != "" {
			references = originalEmail.InReplyTo
		}

		// Build reaction message with threading headers
		// markdown used for both text/plain and text/html parts (same as regular replies)
		raw, err := smtp.BuildReactionMessage(
			from, to, "", subject,
			bodyMarkdown,
			originalEmail.MessageID,
			references,
		)
		if err != nil {
			return sendDoneMsg{err: fmt.Errorf("build reaction: %w", err)}
		}

		// Send via SMTP
		toAddrs := []string{extractEmailAddr(to)}
		if err := smtp.SendRaw(cfg, toAddrs, raw); err != nil {
			return sendDoneMsg{err: err}
		}

		// Save copy to Sent folder (non-fatal if it fails)
		if saveErr := cli.SaveSent(nil, sentFolder, raw); saveErr != nil {
			return sendDoneMsg{
				warning:       "Sent, but failed to save to Sent folder: " + saveErr.Error(),
				replyToUID:    originalEmail.UID,
				replyToFolder: originalEmail.Folder,
			}
		}

		// Mark original email as \Answered (non-fatal)
		if originalEmail.UID > 0 && originalEmail.Folder != "" {
			_ = replyCli.MarkAnswered(nil, originalEmail.Folder, originalEmail.UID)
		}

		return sendDoneMsg{replyToUID: originalEmail.UID, replyToFolder: originalEmail.Folder}
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

func normalizedSender(from string) string {
	return strings.ToLower(extractEmailAddr(from))
}

func writeAttachmentsTemp(files []imap.Attachment) ([]string, error) {
	paths := make([]string, 0, len(files))
	for _, a := range files {
		base := filepath.Base(a.Filename)
		if base == "." || base == string(filepath.Separator) || base == "" {
			base = "attachment"
		}
		f, err := os.CreateTemp(neomdTempDir(), "draft-"+base+"-*")
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(a.Data); err != nil {
			f.Close()
			os.Remove(f.Name())
			return nil, err
		}
		if err := f.Close(); err != nil {
			os.Remove(f.Name())
			return nil, err
		}
		paths = append(paths, f.Name())
	}
	return paths, nil
}

// validateScreenerSafety wraps the shared screener validation logic.
func (m Model) validateScreenerSafety() error {
	return screener.ValidateScreenerSafety(m.cfg.Folders)
}

func (m Model) inboxPageStep() int {
	if m.height <= 8 {
		return 10
	}
	return m.height - 6
}

func (m Model) hasComposeDraft() bool {
	if m.pendingSend != nil {
		if strings.TrimSpace(m.pendingSend.to) != "" ||
			strings.TrimSpace(m.pendingSend.cc) != "" ||
			strings.TrimSpace(m.pendingSend.bcc) != "" ||
			strings.TrimSpace(m.pendingSend.subject) != "" ||
			strings.TrimSpace(m.pendingSend.body) != "" {
			return true
		}
	}
	if strings.TrimSpace(m.compose.to.Value()) != "" ||
		strings.TrimSpace(m.compose.cc.Value()) != "" ||
		strings.TrimSpace(m.compose.bcc.Value()) != "" ||
		strings.TrimSpace(m.compose.subject.Value()) != "" {
		return true
	}
	return len(m.attachments) > 0
}

func (m *Model) beginDiscardConfirm() {
	m.pendingDiscard = true
	m.status = "Discard unsent message?  · y discard, n keep editing"
	m.isError = true
}

func (m *Model) cancelDiscardConfirm() {
	m.pendingDiscard = false
	if m.status == "Discard unsent message?  · y discard, n keep editing" {
		m.status = ""
		m.isError = false
	}
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
		if err := m.validateScreenerSafety(); err != nil {
			return batchDoneMsg{err: err}
		}
		expandedOps := ops
		if len(emails) == 1 && len(m.markedUIDs) == 0 && emails[0].Folder == cfg.Folders.ToScreen {
			sender := normalizedSender(emails[0].From)
			uids, err := m.imapCli().SearchUIDs(nil, cfg.Folders.ToScreen)
			if err != nil {
				return batchDoneMsg{err: err}
			}
			expandedOps = nil
			for start := 0; start < len(uids); start += 200 {
				end := start + 200
				if end > len(uids) {
					end = len(uids)
				}
				batch, err := m.imapCli().FetchHeadersByUID(nil, cfg.Folders.ToScreen, uids[start:end])
				if err != nil {
					return batchDoneMsg{err: err}
				}
				for _, e := range batch {
					if normalizedSender(e.From) == sender {
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
						expandedOps = append(expandedOps, op{e.From, e.Folder, e.UID, dst})
					}
				}
			}
		}
		snapshot := sc.Snapshot()
		seenSenders := make(map[string]bool)
		for _, o := range expandedOps {
			sender := normalizedSender(o.from)
			if seenSenders[sender] {
				continue
			}
			seenSenders[sender] = true
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
				_ = sc.Restore(snapshot)
				return batchDoneMsg{err: err}
			}
		}
		var undos []undoMove
		for i, o := range expandedOps {
			if o.dst != "" && o.dst != o.srcFolder {
				destUID, err := m.imapCli().MoveMessage(nil, o.srcFolder, o.uid, o.dst)
				if err != nil {
					var rollbackErrs []string
					for j := len(undos) - 1; j >= 0; j-- {
						u := undos[j]
						if _, undoErr := m.imapCli().MoveMessage(nil, u.toFolder, u.uid, u.fromFolder); undoErr != nil {
							rollbackErrs = append(rollbackErrs, fmt.Sprintf("%s:%d→%s (%v)", u.toFolder, u.uid, u.fromFolder, undoErr))
						}
					}
					if restoreErr := sc.Restore(snapshot); restoreErr != nil {
						rollbackErrs = append(rollbackErrs, "screener restore: "+restoreErr.Error())
					}
					if len(rollbackErrs) > 0 {
						return batchDoneMsg{err: fmt.Errorf("stopped after %d/%d: %w (rollback failed: %s)", i, len(expandedOps), err, strings.Join(rollbackErrs, "; "))}
					}
					return batchDoneMsg{err: fmt.Errorf("stopped after %d/%d: %w", i, len(expandedOps), err)}
				}
				undos = append(undos, undoMove{uid: destUID, fromFolder: o.srcFolder, toFolder: o.dst})
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
	screenMoves, err := screener.ClassifyForScreen(m.screener, emails, m.cfg.Folders)
	if err != nil {
		return nil
	}
	// Convert screener.ScreenMove to UI autoScreenMove
	moves := make([]autoScreenMove, len(screenMoves))
	for i, sm := range screenMoves {
		moves[i] = autoScreenMove{email: sm.Email, dst: sm.Dst}
	}
	return moves
}

// execDomainScreen applies a confirmed domain-level screener mutation
// (Approve or Block on a "@domain" entry). Reloads the active folder so the
// view reflects any senders that have just been reclassified.
func (m Model) execDomainScreen(op *pendingDomainAction) (tea.Model, tea.Cmd) {
	var err error
	switch op.action {
	case "I":
		err = m.screener.Approve(op.entry)
	case "O":
		err = m.screener.Block(op.entry)
	default:
		m.status = "internal error: unknown domain action"
		m.isError = true
		return m, nil
	}
	if err != nil {
		m.status = fmt.Sprintf("Domain screen failed: %v", err)
		m.isError = true
		return m, nil
	}
	verb := "approved"
	if op.action == "O" {
		verb = "blocked"
	}
	m.status = fmt.Sprintf("Domain %s %s.", op.entry, verb)
	m.loading = true
	return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))
}

// maybeNotifyInbox dispatches desktop notifications for the freshly fetched
// emails. moves describes where each email is heading after auto-screening
// (pass nil if no auto-screen pass ran). folder is the IMAP source-folder
// name the fetch came from. No-op when the notifier is disabled.
//
// All folder values are converted to UI labels ("Inbox", "PaperTrail", …)
// before the notifier sees them so [notifications].folders works regardless
// of whether the user kept default IMAP names or set custom ones (e.g.
// folders.papertrail = "HEY/Paper Trail").
//
// Reports activity on the TUI status bar so the user can confirm the pipeline
// is wired up — notifications themselves are easy to miss.
func (m *Model) maybeNotifyInbox(folder string, emails []imap.Email, moves []autoScreenMove) {
	if !m.notifier.Enabled() || len(emails) == 0 {
		return
	}
	folderLabel := m.cfg.Folders.LabelFor(folder)
	dstByUID := make(map[uint32]string, len(moves))
	for _, mv := range moves {
		if mv.email != nil {
			dstByUID[mv.email.UID] = m.cfg.Folders.LabelFor(mv.dst)
		}
	}
	// State key uses the IMAP folder name (stable across upgrades — the
	// label may change as we rename folders or add aliases). The label is
	// only used for the allowlist comparison.
	res := m.notifier.MaybeNotify(m.activeAccount().Name, folder, folderLabel, emails, dstByUID, m.screener, m.notifyState)
	switch {
	case res.Failed > 0:
		m.status = fmt.Sprintf("Notification command failed (%d): %s", res.Failed, res.Err)
		m.isError = true
	case res.Sent > 0:
		m.status = fmt.Sprintf("Notified %d VIP sender(s) in %s.", res.Sent, folderLabel)
	case !res.HadBaseline:
		m.status = fmt.Sprintf("Notify baseline set for %s (UID %d). New mail from now on will notify.", folderLabel, res.MaxUIDObserved)
	case res.NewSinceBase == 0:
		// No new emails — silent (avoid noisy status on every refresh).
	case res.MatchedNotify == 0:
		m.status = fmt.Sprintf("Notify check %s: %d new email(s), 0 from VIPs (baseline UID %d → %d).",
			folderLabel, res.NewSinceBase, res.Baseline, res.MaxUIDObserved)
	case res.FolderAllowed == 0:
		m.status = fmt.Sprintf("Notify check %s: %d VIP email(s), but destination not in allowlist (folders=%v).",
			folderLabel, res.MatchedNotify, m.cfg.Notifications.Resolved().Folders)
	}
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

// spyScanCmd scans all emails in the current folder for spy pixels.
// Fetches the full UID list from the server, skips already-scanned UIDs,
// and returns results via message for the Update loop to merge.
func (m Model) spyScanCmd() tea.Cmd {
	folder := m.activeFolder()
	cli := m.imapCli()
	// Copy scanned set to avoid concurrent read.
	alreadyScanned := make(map[string]bool, len(m.spyScannedKeys))
	for k, v := range m.spyScannedKeys {
		alreadyScanned[k] = v
	}

	return func() tea.Msg {
		// Fetch all UIDs in the folder from the server.
		allUIDs, err := cli.SearchUIDs(nil, folder)
		if err != nil {
			return spyScanProgressMsg{err: err}
		}
		// Filter to only unscanned UIDs.
		var uids []uint32
		for _, uid := range allUIDs {
			if !alreadyScanned[spyPixelKey(folder, uid)] {
				uids = append(uids, uid)
			}
		}
		if len(uids) == 0 {
			return spyScanProgressMsg{done: true}
		}

		total := len(uids)
		var spyFound []string
		var allScanned []string
		found := 0
		for i, uid := range uids {
			spy, err := cli.ScanSpyPixels(nil, folder, uid)
			if err != nil {
				return spyScanProgressMsg{err: err, scanned: i, total: total, found: found,
					spyKeys: spyFound, scannedKeys: allScanned}
			}
			key := spyPixelKey(folder, uid)
			allScanned = append(allScanned, key)
			if spy.Count > 0 {
				found++
				spyFound = append(spyFound, key)
			}
		}
		return spyScanProgressMsg{
			scanned: total, total: total, found: found, done: true,
			spyKeys: spyFound, scannedKeys: allScanned,
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

// scheduleMarkAsReadTimer returns a Cmd that fires markAsReadTimerMsg after the configured
// delay. Returns nil (no-op) when mark_as_read_after_secs = 0 (immediate marking).
func (m Model) scheduleMarkAsReadTimer(uid uint32, folder string) tea.Cmd {
	secs := m.cfg.UI.MarkAsReadAfterSecs
	if secs <= 0 {
		return nil // immediate marking handled elsewhere
	}
	return tea.Tick(time.Duration(secs)*time.Second, func(time.Time) tea.Msg {
		return markAsReadTimerMsg{uid: uid, folder: folder}
	})
}

// bgFetchInboxCmd silently fetches inbox headers for background screening.
// Errors are swallowed — a transient network hiccup shouldn't disrupt the UI.
func (m Model) bgFetchInboxCmd() tea.Cmd {
	return func() tea.Msg {
		m.imapCli().ResetMailboxSelection() // force fresh SELECT to see new messages
		emails, err := m.imapCli().FetchHeaders(nil, m.cfg.Folders.Inbox, m.cfg.UI.InboxCount)
		if err != nil {
			// Return nil to let the next scheduled tick retry naturally.
			// Returning bgSyncTickMsg{} here creates an infinite loop on persistent errors!
			return bgInboxFetchedMsg{emails: nil} // signal completion even on error
		}
		return bgInboxFetchedMsg{emails: emails}
	}
}

// bgFetchVipFolderCmd silently fetches headers from one configured
// notification folder (other than Inbox, which is handled by the regular bg
// sync). The result feeds straight into the notifier — no auto-screening,
// no UI mutation beyond a brief status update.
func (m Model) bgFetchVipFolderCmd(folder string) tea.Cmd {
	return func() tea.Msg {
		cli := m.imapCli()
		if cli == nil {
			return bgVipFolderFetchedMsg{folder: folder, emails: nil}
		}
		cli.ResetMailboxSelection()
		emails, err := cli.FetchHeaders(nil, folder, m.cfg.UI.InboxCount)
		if err != nil {
			return bgVipFolderFetchedMsg{folder: folder, emails: nil}
		}
		return bgVipFolderFetchedMsg{folder: folder, emails: emails}
	}
}

// vipNotifyFolders returns the configured non-Inbox folder labels (mapped to
// IMAP folder names) that should be polled for VIP notifications by the
// background sync. Empty when the notifier is disabled or only "Inbox" is set.
func (m Model) vipNotifyFolders() []string {
	if !m.notifier.Enabled() {
		return nil
	}
	cfg := m.cfg.Notifications.Resolved()
	var out []string
	for _, label := range cfg.Folders {
		if strings.EqualFold(label, "Inbox") {
			continue // Inbox is already covered by bgFetchInboxCmd
		}
		if dst := folderLabelToIMAP(label, m.cfg.Folders); dst != "" {
			out = append(out, dst)
		}
	}
	return out
}

// folderLabelToIMAP maps a UI folder label (e.g. "PaperTrail") to its
// configured IMAP folder name. Returns "" for unknown labels.
func folderLabelToIMAP(label string, fc config.FoldersConfig) string {
	switch strings.ToLower(label) {
	case "inbox":
		return fc.Inbox
	case "sent":
		return fc.Sent
	case "trash":
		return fc.Trash
	case "drafts":
		return fc.Drafts
	case "toscreen":
		return fc.ToScreen
	case "feed":
		return fc.Feed
	case "papertrail":
		return fc.PaperTrail
	case "screenedout":
		return fc.ScreenedOut
	case "archive":
		return fc.Archive
	case "waiting":
		return fc.Waiting
	case "scheduled":
		return fc.Scheduled
	case "someday":
		return fc.Someday
	case "spam":
		return fc.Spam
	case "work":
		return fc.Work
	}
	return ""
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
					m.imapSearchText = ""
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
		if m.status == "" && m.startupNotice != "" {
			m.status = m.startupNotice
			m.startupNotice = ""
		}
		sortCmd := m.sortEmails() // applies sort and sets list items

		// mailto: open compose with pre-filled fields on first inbox load.
		if m.mailto != nil {
			mp := m.mailto
			m.mailto = nil // consume once
			m.attachments = nil
			m.compose.reset()
			m.presendFromI = 0
			if mp.To != "" {
				m.compose.to.SetValue(mp.To)
			}
			if mp.CC != "" {
				m.compose.cc.SetValue(mp.CC)
				m.compose.extraVisible = true
			}
			if mp.BCC != "" {
				m.compose.bcc.SetValue(mp.BCC)
				m.compose.extraVisible = true
			}
			if mp.Subject != "" {
				m.compose.subject.SetValue(mp.Subject)
			}
			m.state = stateCompose
			m.status = ""
			m.isError = false
			m.mailtoBody = mp.Body
			return m, tea.Batch(sortCmd, m.fetchFolderCountsCmd())
		}

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
			if err := m.validateScreenerSafety(); err != nil {
				m.status = err.Error()
				m.isError = true
				return m, tea.Batch(sortCmd, m.fetchFolderCountsCmd())
			}
			if moves := m.previewAutoScreen(); len(moves) > 0 {
				m.maybeNotifyInbox(msg.folder, msg.emails, moves)
				m.loading = true
				m.bulkProgress = m.newBulkOp("Screening", len(moves))
				return m, tea.Batch(sortCmd, m.fetchFolderCountsCmd(), m.spinner.Tick, m.execAutoScreenCmd(moves))
			}
		}
		// Notify on any folder load that matches the user's allowlist — not
		// just Inbox. Covers the case where a headless daemon has already
		// screened a VIP email out of Inbox into PaperTrail/Feed before the
		// TUI sees it; the notification still fires when the user (or the
		// next refresh) loads that destination folder.
		//
		// Convert the IMAP folder name to its UI label first so allowlists
		// written with labels (e.g. "PaperTrail") still match when the IMAP
		// folder name is custom (e.g. "HEY/Paper Trail").
		if m.cfg.Notifications.FolderAllowed(m.cfg.Folders.LabelFor(msg.folder)) {
			m.maybeNotifyInbox(msg.folder, msg.emails, nil)
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
		m.openSpyPixels = msg.spyPixels
		// Track spy pixel presence for inbox indicator
		if msg.email != nil {
			key := spyPixelKey(msg.email.Folder, msg.email.UID)
			if msg.spyPixels.Count > 0 {
				m.spyPixelKeys[key] = true
			}
			if !m.spyScannedKeys[key] {
				m.spyScannedKeys[key] = true
				safeGo(func() { saveSpyPixelCache(copyMap(m.spyPixelKeys), copyMap(m.spyScannedKeys)) })
			}
		}
		// Store References header in the email struct for threading
		if msg.email != nil {
			msg.email.References = msg.references
		}
		// Mark as seen: either immediately (if config = 0) or after timer
		uid := msg.email.UID
		folder := msg.email.Folder
		markImmediately := func() {
			safeGo(func() { _ = m.imapCli().MarkSeen(nil, folder, uid) })
			// Update local state immediately
			for i := range m.emails {
				if m.emails[i].UID == uid && m.emails[i].Folder == folder {
					m.emails[i].Seen = true
					break
				}
			}
		}
		if m.cfg.UI.MarkAsReadAfterSecs <= 0 {
			// Immediate marking (config = 0)
			markImmediately()
		} else {
			// Schedule timer-based marking (for normal reading flow only)
			m.markAsReadUID = uid
			m.markAsReadFolder = folder
		}
		// Handle pending actions - always mark immediately before launching
		if m.pendingForward || m.pendingReply || m.pendingReplyAll || m.pendingReaction {
			// Mark as read immediately when launching compose actions (preserves original behavior)
			if m.cfg.UI.MarkAsReadAfterSecs > 0 {
				markImmediately()
			}
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
			if m.pendingReaction {
				m.pendingReaction = false
				return m.enterReactionMode(msg.email)
			}
		}
		m.openLinks = extractLinks(msg.body)
		_ = loadEmailIntoReader(&m.reader, msg.email, msg.body, msg.attachments, m.openSpyPixels, m.openLinks, glamourStyleFor(m.cfg.UI.Theme), m.width)
		m.state = stateReading
		// Refresh inbox list if immediate mode, or start timer
		if m.cfg.UI.MarkAsReadAfterSecs <= 0 {
			return m, m.applyFilter()
		} else {
			return m, m.scheduleMarkAsReadTimer(uid, folder)
		}

	case sendDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.status = msg.err.Error()
			m.isError = true
		} else if msg.warning != "" {
			m.status = msg.warning
			m.isError = true // show in red so user notices
			m.state = stateInbox
		} else if msg.info != "" {
			m.status = msg.info
			m.isError = false
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
		} else if msg.dangerous {
			m.status = "Saved to " + msg.path + " — not auto-opened (" + msg.reason + ")"
			m.isError = true
		} else {
			m.status = "Saved to " + msg.path + " — opening…"
			m.isError = false
		}
		return m, nil

	case emlDownloadedMsg:
		if msg.err != nil {
			m.status = "Download error: " + msg.err.Error()
			m.isError = true
		} else {
			m.status = "Saved EML to " + msg.path
			m.isError = false
		}
		return m, nil

	case rsvpDoneMsg:
		if msg.err != nil {
			m.status = "RSVP send failed: " + msg.err.Error()
			m.isError = true
		} else {
			m.status = "RSVP sent: " + strings.ToLower(string(msg.status))
			m.isError = false
		}
		return m, nil

	case icsOpenedMsg:
		if msg.err != nil {
			m.status = "Open .ics failed: " + msg.err.Error()
			m.isError = true
		} else {
			m.status = "Opened " + msg.path
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

	case markAsReadTimerMsg:
		// Timer fired - mark email as read if user is still viewing it
		if m.state == stateReading && m.markAsReadUID == msg.uid && m.markAsReadFolder == msg.folder {
			// Still viewing the same email - mark it as read
			safeGo(func() { _ = m.imapCli().MarkSeen(nil, msg.folder, msg.uid) })
			// Update local state immediately
			for i := range m.emails {
				if m.emails[i].UID == msg.uid && m.emails[i].Folder == msg.folder {
					m.emails[i].Seen = true
					break
				}
			}
			// Clear timer state
			m.markAsReadUID = 0
			m.markAsReadFolder = ""
			return m, m.applyFilter()
		}
		// User navigated away - ignore timer
		return m, nil

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

	case spyScanProgressMsg:
		// Merge results into maps on the main goroutine (no concurrency).
		for _, k := range msg.spyKeys {
			m.spyPixelKeys[k] = true
		}
		for _, k := range msg.scannedKeys {
			m.spyScannedKeys[k] = true
		}
		if msg.err != nil {
			m.status = "Spy scan error: " + msg.err.Error()
			m.isError = true
		} else if msg.done && msg.total == 0 {
			m.status = "Spy scan: all emails already scanned"
			m.isError = false
		} else if msg.done {
			m.status = fmt.Sprintf("Spy scan complete: %d/%d emails had tracking pixels", msg.found, msg.scanned)
			m.isError = false
		}
		// Save cache and rebuild inbox on the main goroutine.
		if len(msg.scannedKeys) > 0 {
			safeGo(func() { saveSpyPixelCache(copyMap(m.spyPixelKeys), copyMap(m.spyScannedKeys)) })
		}
		return m, m.applyFilter()

	case deepScreenCountMsg:
		if err := m.validateScreenerSafety(); err != nil {
			m.loading = false
			m.status = err.Error()
			m.isError = true
			return m, nil
		}
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
		// Skip if a background sync is already in progress to prevent pileup.
		if m.bgSyncInProgress {
			return m, m.scheduleBgSync() // just reschedule the next tick
		}
		// Fire background inbox fetch; reschedule next tick in parallel.
		// Also poll any extra notification folders (e.g. PaperTrail, Feed)
		// so VIP mail screened out of Inbox by the daemon still notifies.
		m.bgSyncInProgress = true
		cmds := []tea.Cmd{m.bgFetchInboxCmd(), m.scheduleBgSync()}
		for _, f := range m.vipNotifyFolders() {
			cmds = append(cmds, m.bgFetchVipFolderCmd(f))
		}
		return m, tea.Batch(cmds...)

	case bgInboxFetchedMsg:
		// Keep bgSyncInProgress set until the entire fetch-and-screen cycle completes.
		// Clear it only on early-exit paths where no follow-up work is scheduled.
		if msg.emails == nil {
			// Error case (network down, etc.) - silently skip until next tick
			m.bgSyncInProgress = false
			return m, nil
		}
		if err := m.validateScreenerSafety(); err != nil {
			m.status = err.Error()
			m.isError = true
			m.bgSyncInProgress = false
			return m, nil
		}
		moves := m.classifyForScreen(msg.emails)
		m.maybeNotifyInbox(m.cfg.Folders.Inbox, msg.emails, moves)
		if len(moves) == 0 {
			// No moves needed - background sync is complete
			m.bgSyncInProgress = false
			return m, nil
		}
		// bgSyncInProgress stays set - will be cleared in bgScreenDoneMsg
		return m, m.bgExecAutoScreenCmd(moves)

	case bgVipFolderFetchedMsg:
		if msg.emails == nil {
			return m, nil // transient error; next tick retries
		}
		m.maybeNotifyInbox(msg.folder, msg.emails, nil)
		return m, nil

	case bgScreenDoneMsg:
		// Background sync cycle complete - clear the guard flag
		m.bgSyncInProgress = false
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
			m.attachments = nil
			m.status = msg.err.Error()
			m.isError = true
			m.state = stateInbox
			return m, nil
		}
		if msg.aborted {
			m.attachments = nil
			m.status = "Aborted (no changes saved). Use :recover to reopen the latest backup."
			m.state = stateInbox
			return m, nil
		}
		if strings.TrimSpace(msg.body) == "" {
			m.attachments = nil
			m.status = "Cancelled (empty body). Use :recover if you want the latest backup."
			m.state = stateInbox
			return m, nil
		}
		// Strip editor header hints and extract [attach] lines.
		// [html-signature] marker is NOT extracted yet — keep it in body for preview.
		inlineAttach, cleanBody := extractInlineAttachments(stripPrelude(msg.body))
		m.attachments = append(m.attachments, inlineAttach...)
		m.applyEditedFrom(msg.from)

		// Go to pre-send review instead of sending immediately.
		m.pendingSend = &pendingSendData{
			to: msg.to, cc: msg.cc, bcc: mergeAutoBCC(msg.bcc, m.cfg.AutoBCC),
			subject: msg.subject, body: cleanBody,
		}
		// Track original email for \Answered flag (replies/forwards).
		if m.openEmail != nil && strings.HasPrefix(strings.ToLower(msg.subject), "re:") {
			m.pendingSend.replyToUID = m.openEmail.UID
			m.pendingSend.replyToFolder = m.openEmail.Folder
			m.pendingSend.replyToAccount = m.activeAccount().Name
			// Populate threading headers for proper email conversation threading
			m.pendingSend.inReplyTo = m.openEmail.MessageID
			// Build References chain: use existing References or fall back to InReplyTo
			if m.openEmail.References != "" {
				m.pendingSend.references = m.openEmail.References
			} else if m.openEmail.InReplyTo != "" {
				// Fall back to InReplyTo if References not available
				m.pendingSend.references = m.openEmail.InReplyTo
			}
		}
		m.state = statePresend
		m.status = ""
		m.isError = false
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
				m.helpSearch = ""
				m.helpSearchActive = false
				m.helpScroll = 0
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
		case stateReaction:
			return m.updateReaction(msg)
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
		m.pendingDomainOp = nil
	}
	m.status = ""
	m.isError = false

	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit

	case "esc":
		if m.filterText != "" || m.showUnreadOnly {
			m.filterActive = false
			m.filterText = ""
			m.showUnreadOnly = false
			return m, m.applyFilter()
		}
		if m.imapSearchResults {
			m.imapSearchResults = false
			m.imapSearchText = ""
			m.offTabFolder = ""
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))
		}
		if m.offTabFolder != "" {
			m.offTabFolder = ""
			// If we have a pending search query, restore search results instead of activeFolder
			if m.imapSearchText != "" {
				m.loading = true
				return m, tea.Batch(m.spinner.Tick, m.imapSearchAllCmd(m.imapSearchText))
			}
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))
		}

	// ── Chord prefixes ──────────────────────────────────────────────
	case "g":
		m.pendingKey = "g"
		m.status = "go to:  gi inbox  ga archive  gf feed  gp papertrail  gt trash  gs sent  gk toscreen  go screened-out  gw waiting  gc scheduled  gm someday  gd drafts  gS spam  ge everything  gg top"
		return m, nil

	case " ": // leader key — wait for digit or shortcut
		m.pendingKey = " "
		m.status = "leader:  1-9 folder tab  / IMAP search  S scan spy pixels  w welcome  (esc to cancel)"
		return m, nil

	case "M":
		m.pendingKey = "M"
		m.status = "move to:  Mi inbox  Ma archive  Mf feed  Mp papertrail  Mt trash  Mo screened-out  Mw waiting  Mc scheduled  Mm someday"
		return m, nil

	case "D":
		m.pendingKey = "D"
		m.status = "domain screen:  Di in (@domain → screened_in)  Do out (@domain → screened_out)  (esc to cancel)"
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

	// B = move to Work/Business (pure move, no screener update)
	case "B":
		if m.cfg.Folders.Work == "" {
			m.status = "Work folder not configured"
			m.isError = true
			return m, nil
		}
		targets := m.targetEmails()
		if len(targets) == 0 {
			return m, nil
		}
		m.loading = true
		m.bulkProgress = m.newBulkOp("Moving to Work", len(targets))
		return m, tea.Batch(m.spinner.Tick, m.batchMoveCmd(targets, m.cfg.Folders.Work))

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
		if err := m.validateScreenerSafety(); err != nil {
			m.status = err.Error()
			m.isError = true
			return m, nil
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
		if m.pendingDomainOp != nil {
			op := m.pendingDomainOp
			m.pendingDomainOp = nil
			return m.execDomainScreen(op)
		}
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
		if m.pendingDomainOp != nil {
			m.pendingDomainOp = nil
			m.status = "Cancelled."
			return m, nil
		}
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

	case "N":
		// Jump to next unread email
		current := m.inbox.Index()
		items := m.inbox.Items()
		for i := current + 1; i < len(items); i++ {
			if item, ok := items[i].(emailItem); ok {
				if !item.email.Seen {
					m.inbox.Select(i)
					return m, nil
				}
			}
		}
		// Wrap around to beginning
		for i := 0; i <= current; i++ {
			if item, ok := items[i].(emailItem); ok {
				if !item.email.Seen {
					m.inbox.Select(i)
					return m, nil
				}
			}
		}
		m.status = "No unread emails found."
		return m, nil

	// ── Navigation ──────────────────────────────────────────────────
	case "tab", "L", "]":
		m.activeFolderI = (m.activeFolderI + 1) % len(m.folders)
		m.offTabFolder = ""
		m.imapSearchResults = false
		m.imapSearchText = ""
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))

	case "shift+tab", "H", "[":
		m.activeFolderI = (m.activeFolderI - 1 + len(m.folders)) % len(m.folders)
		m.offTabFolder = ""
		m.imapSearchResults = false
		m.imapSearchText = ""
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))

	case "G":
		m.inbox.Select(len(m.inbox.Items()) - 1)
		return m, nil

	case "d":
		next := m.inbox.Index() + m.inboxPageStep()
		if max := len(m.inbox.Items()) - 1; next > max {
			next = max
		}
		if next >= 0 {
			m.inbox.Select(next)
		}
		return m, nil

	case "u":
		prev := m.inbox.Index() - m.inboxPageStep()
		if prev < 0 {
			prev = 0
		}
		m.inbox.Select(prev)
		return m, nil

	case "/":
		m.filterActive = true
		m.filterText = ""
		return m, m.applyFilter()

	case "z":
		// Toggle unread-only filter
		m.showUnreadOnly = !m.showUnreadOnly
		if m.showUnreadOnly {
			m.status = "Showing unread only · z to show all"
		} else {
			m.status = "Showing all emails"
		}
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
			// Skip IMAP-disabled accounts (nil clients).
			for range m.clients {
				m.accountI = (m.accountI + 1) % len(m.clients)
				if m.clients[m.accountI] != nil {
					break
				}
			}
			m.activeFolderI = 0
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))
		}

	case "c":
		m.attachments = nil
		m.state = stateCompose
		m.status = ""
		m.isError = false
		m.compose.reset()
		m.presendFromI = 0
		return m, nil

	case "R":
		m.imapCli().ResetMailboxSelection() // force fresh SELECT to see new messages
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

	case "ctrl+e":
		e := selectedEmail(m.inbox)
		if e == nil {
			return m, nil
		}
		if idx := m.matchFromIndex(e.To, e.CC); idx >= 0 {
			m.presendFromI = idx
		}
		// Always fetch body first (needed for quoted message in reaction)
		m.pendingReaction = true
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
		cmp := compareEmails(m.emails[i], m.emails[j], field)
		// Apply sort direction
		if rev {
			return cmp > 0 // descending: a > b means a comes first
		}
		return cmp < 0 // ascending: a < b means a comes first
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

// loadSpyPixelCache reads the spy pixel cache from disk.
// Lines prefixed with "+" have spy pixels, "-" were scanned clean.
func loadSpyPixelCache() (spyKeys, scannedKeys map[string]bool) {
	spyKeys = make(map[string]bool)
	scannedKeys = make(map[string]bool)
	data, err := os.ReadFile(config.SpyPixelCachePath())
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if len(line) < 2 {
			continue
		}
		key := line[1:]
		switch line[0] {
		case '+':
			spyKeys[key] = true
			scannedKeys[key] = true
		case '-':
			scannedKeys[key] = true
		}
	}
	return
}

// saveSpyPixelCache writes a snapshot of the spy pixel cache to disk.
// Takes copied maps to avoid concurrent access.
func saveSpyPixelCache(spyKeys, scannedKeys map[string]bool) {
	var lines []string
	for k := range scannedKeys {
		if spyKeys[k] {
			lines = append(lines, "+"+k)
		} else {
			lines = append(lines, "-"+k)
		}
	}
	_ = os.WriteFile(config.SpyPixelCachePath(), []byte(strings.Join(lines, "\n")+"\n"), 0600)
}

// safeGo runs fn in a goroutine with panic recovery. If the goroutine panics,
// the stack trace is logged to stderr and written to ~/.cache/neomd/crash.log.
func safeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				log.Printf("goroutine panic recovered: %v\n%s", r, stack)
				writeCrashLog(r, stack)
			}
		}()
		fn()
	}()
}

// writeCrashLog appends a panic record to the crash log file.
func writeCrashLog(r interface{}, stack []byte) {
	path := config.CrashLogPath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "=== neomd crash at %s ===\npanic: %v\n%s\n\n", time.Now().Format(time.RFC3339), r, stack)
}

// copyMap returns a shallow copy of a map, safe for passing to goroutines.
func copyMap(m map[string]bool) map[string]bool {
	c := make(map[string]bool, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func (m Model) shouldPrefixFolderInSubject() bool {
	switch m.offTabFolder {
	case "Search", "Everything", "Thread":
		return true
	default:
		return false
	}
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

// applyFilter filters m.emails by filterText and showUnreadOnly and refreshes the list.
// Call this whenever filterText or showUnreadOnly changes.
func (m *Model) applyFilter() tea.Cmd {
	var filtered []imap.Email

	// Apply both text filter and unread filter
	for _, e := range m.emails {
		// Skip if unread-only mode is on and email is read
		if m.showUnreadOnly && e.Seen {
			continue
		}

		// Skip if text filter is active and doesn't match
		if m.filterText != "" {
			query := strings.ToLower(m.filterText)
			hay := strings.ToLower(e.From + " " + e.Subject)
			if !strings.Contains(hay, query) {
				continue
			}
		}

		filtered = append(filtered, e)
	}

	// If no filters are active, use all emails
	if m.filterText == "" && !m.showUnreadOnly {
		filtered = m.emails
	}

	noThread := len(m.folders) > 0 && m.activeFolder() == m.cfg.Folders.Sent
	return setEmails(&m.inbox, filtered, m.markedUIDs, m.spyPixelKeys, m.shouldPrefixFolderInSubject(), m.sortField, m.sortReverse, noThread)
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
		if key == "w" {
			m.state = stateWelcome
			return m, nil
		}
		if key == "S" {
			m.status = "Scanning for spy pixels…"
			return m, m.spyScanCmd()
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
			m.imapSearchText = ""
			m.status = "Spam folder — press R to reload, tab to leave"
			return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.cfg.Folders.Spam))
		}
		if key == "d" { // gd — go to Drafts (not in tab rotation)
			m.loading = true
			m.offTabFolder = "Drafts"
			m.imapSearchText = ""
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
			"c": "Scheduled",
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
					m.imapSearchText = ""
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
			"c": m.cfg.Folders.Scheduled,
			"m": m.cfg.Folders.Someday,
			"k": m.cfg.Folders.ToScreen,
		}
		// Only add Work folder if configured
		if m.cfg.Folders.Work != "" {
			dstMap["b"] = m.cfg.Folders.Work
		}
		if dst, ok := dstMap[key]; ok {
			m.loading = true
			m.bulkProgress = m.newBulkOp("Moving", len(targets))
			return m, tea.Batch(m.spinner.Tick, m.batchMoveCmd(targets, dst))
		}
		m.status = fmt.Sprintf("unknown: M%s", key)

	case "D":
		if key != "i" && key != "o" {
			m.status = fmt.Sprintf("unknown: D%s  (use Di or Do)", key)
			return m, nil
		}
		e := selectedEmail(m.inbox)
		if e == nil {
			m.status = "No email selected for domain action."
			m.isError = true
			return m, nil
		}
		entry := domainEntry(e.From)
		if entry == "" {
			m.status = "Selected email has no parseable domain."
			m.isError = true
			return m, nil
		}
		action := strings.ToUpper(key)
		m.pendingDomainOp = &pendingDomainAction{entry: entry, action: action}
		verb := "IN"
		if action == "O" {
			verb = "OUT"
		}
		m.status = fmt.Sprintf("Screen %s domain %s? (y/n) — affects every future sender at this domain", verb, entry)
		return m, nil

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
		case " ": // space + digit opens link (1-10)
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
			// space + l = prefix for links 11-99 (two digits)
			if key == "l" {
				m.readerPending = "l"
				m.status = "link number (11-99): l__"
				return m, nil
			}
			// space + d = download raw EML source
			if key == "d" {
				m.status = "Downloading EML…"
				m.isError = false
				return m, m.downloadEMLCmd()
			}
			// space + v = leader for calendar RSVP chord (v + a/d/t/o)
			if key == "v" {
				if m.calendarInvite() == nil {
					m.status = "No calendar invite in this email."
					return m, nil
				}
				m.readerPending = "v"
				m.status = "rsvp: a=accept · d=decline · t=tentative · o=open in calendar app"
				return m, nil
			}
			// space + n / N = add open email's sender (or @domain) to notify.txt
			if key == "n" || key == "N" {
				if m.openEmail == nil {
					return m, nil
				}
				entry := strings.ToLower(extractEmailAddr(m.openEmail.From))
				if key == "N" {
					entry = domainEntry(m.openEmail.From)
				}
				if entry == "" {
					m.status = "Could not parse sender address."
					m.isError = true
					return m, nil
				}
				if err := m.screener.AddNotify(entry); err != nil {
					m.status = "Add to notify.txt failed: " + err.Error()
					m.isError = true
					return m, nil
				}
				m.status = fmt.Sprintf("Added %s to notify.txt — desktop notifications will fire for new mail from this %s.",
					entry, map[bool]string{true: "domain", false: "sender"}[key == "N"])
				return m, nil
			}
			// Not a digit or 'l' — fall through
		case "l": // l + first digit (waiting for second digit)
			if len(key) == 1 && key >= "0" && key <= "9" {
				m.readerPending = "l" + key
				m.status = fmt.Sprintf("link number: l%s_", key)
				return m, nil
			}
			// Not a digit — fall through
		case "g": // gg = top of email
			if key == "g" {
				m.reader.GotoTop()
				return m, nil
			}
		case "v": // <space> v {a|d|t|o} — calendar RSVP chord
			switch key {
			case "a":
				return m, m.sendRSVPCmd(calendar.StatusAccepted)
			case "d":
				return m, m.sendRSVPCmd(calendar.StatusDeclined)
			case "t":
				return m, m.sendRSVPCmd(calendar.StatusTentative)
			case "o":
				return m, m.openICSCmd()
			default:
				m.status = fmt.Sprintf("rsvp: unknown chord v%s — use a/d/t/o or esc", key)
				m.isError = true
				return m, nil
			}
		case "D": // Di / Do = domain-level screen IN / OUT for the open email
			if key != "i" && key != "o" {
				m.status = fmt.Sprintf("unknown: D%s  (use Di or Do)", key)
				m.isError = true
				return m, nil
			}
			if m.openEmail == nil {
				return m, nil
			}
			entry := domainEntry(m.openEmail.From)
			if entry == "" {
				m.status = "Open email has no parseable domain."
				m.isError = true
				return m, nil
			}
			action := strings.ToUpper(key)
			m.pendingDomainOp = &pendingDomainAction{entry: entry, action: action}
			verb := "IN"
			if action == "O" {
				verb = "OUT"
			}
			m.status = fmt.Sprintf("Screen %s domain %s? (y/n) — affects every future sender at this domain", verb, entry)
			return m, nil
		default:
			// Handle "l[0-9]" pattern (first digit entered, waiting for second)
			if len(pending) == 2 && pending[0] == 'l' && pending[1] >= '0' && pending[1] <= '9' {
				if len(key) == 1 && key >= "0" && key <= "9" {
					// Parse two-digit number
					numStr := string(pending[1]) + key
					num, _ := strconv.Atoi(numStr)
					idx := num - 1 // convert to 0-based index
					if idx >= 0 && idx < len(m.openLinks) {
						return m, m.openLinkCmd(m.openLinks[idx].URL)
					}
					m.status = fmt.Sprintf("No link [%d].", num)
					return m, nil
				}
			}
		}
	}

	// Pending domain-screen confirmation (set by Di / Do chord) — only y/n accepted.
	if m.pendingDomainOp != nil {
		switch key {
		case "y":
			op := m.pendingDomainOp
			m.pendingDomainOp = nil
			return m.execDomainScreen(op)
		case "n", "esc":
			m.pendingDomainOp = nil
			m.status = "Cancelled."
			return m, nil
		default:
			// Any other key cancels the confirmation but does not consume the key.
			m.pendingDomainOp = nil
			m.status = ""
		}
	}

	switch key {
	case "q", "esc", "h":
		m.state = stateInbox
		m.readerPending = ""
		// Clear mark-as-read timer state when exiting reader
		m.markAsReadUID = 0
		m.markAsReadFolder = ""
		// Rebuild inbox list so ⊙ spy pixel indicator appears immediately
		if m.openSpyPixels.Count > 0 {
			return m, m.applyFilter()
		}
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
		m.imapCli().ResetMailboxSelection() // force fresh SELECT to see new messages
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))
	case "ctrl+r":
		if m.openEmail != nil {
			return m.launchReplyAllCmd()
		}
	case "ctrl+e":
		if m.openEmail != nil {
			return m.enterReactionMode(m.openEmail)
		}
	case "f":
		if m.openEmail != nil {
			return m.launchForwardCmd()
		}
	case "T":
		if m.openEmail != nil {
			m.loading = true
			m.state = stateInbox
			// Clear mark-as-read timer state when switching to conversation view
			m.markAsReadUID = 0
			m.markAsReadFolder = ""
			return m, tea.Batch(m.spinner.Tick, m.fetchConversationCmd(m.openEmail))
		}
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx := int(msg.String()[0] - '1') // 0-based
		if idx < len(m.openAttachments) {
			return m, m.downloadOpenAttachmentCmd(m.openAttachments[idx])
		}
	case " ":
		m.readerPending = " "
		var hints []string
		if len(m.openLinks) > 10 {
			hints = append(hints, "1-0 links", "l11-99 links 11+")
		} else if len(m.openLinks) > 0 {
			hints = append(hints, "1-0 links")
		}
		hints = append(hints, "d download .eml", "n add sender to notify.txt", "N add @domain to notify.txt")
		m.status = "space: " + strings.Join(hints, "  ·  ")
		return m, nil
	case "g":
		m.readerPending = "g"
		return m, nil
	case "G":
		m.reader.GotoBottom()
		return m, nil
	case "D":
		m.readerPending = "D"
		m.status = "domain screen:  Di in (@domain → screened_in)  Do out (@domain → screened_out)  (esc to cancel)"
		return m, nil
	}
	var cmd tea.Cmd
	m.reader, cmd = m.reader.Update(msg)
	return m, cmd
}

// openLinkCmd opens a URL in $BROWSER (or xdg-open).
// Only http://, https://, and mailto: schemes are allowed to prevent
// javascript:, data:, or other potentially dangerous URL schemes.
func (m Model) openLinkCmd(url string) tea.Cmd {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "mailto:") {
		return func() tea.Msg {
			return errMsg{fmt.Errorf("blocked unsafe URL scheme: %s", url)}
		}
	}
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
		htmlBody = render.SanitizeForBrowser(m.openHTMLBody)
	} else {
		var err error
		htmlBody, err = render.ToHTML(m.openBody)
		if err != nil {
			htmlBody = "<pre>" + m.openBody + "</pre>"
		}
	}

	// Save inline image attachments (Content-ID) to temp files and rewrite
	// cid: references to file:// URLs so the browser can display them.
	var tmpImages []string
	for _, a := range m.openAttachments {
		if a.ContentID == "" || len(a.Data) == 0 {
			continue
		}
		// Sanitize ContentID and Filename to prevent path traversal attacks
		safeCID := strings.ReplaceAll(a.ContentID, string(os.PathSeparator), "_")
		safeCID = strings.ReplaceAll(safeCID, "..", "_")
		safeName := filepath.Base(a.Filename)

		imgPath := filepath.Join(neomdTempDir(), "cid-"+safeCID+"-"+safeName)

		// Verify the path is still under neomdTempDir()
		if !strings.HasPrefix(imgPath, neomdTempDir()) {
			continue
		}

		if err := os.WriteFile(imgPath, a.Data, 0600); err != nil {
			continue
		}
		tmpImages = append(tmpImages, imgPath)
		// Replace cid:XYZ with file:///path (case-sensitive match)
		htmlBody = strings.ReplaceAll(htmlBody, "cid:"+a.ContentID, "file://"+imgPath)
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
		safeGo(func() {
			time.Sleep(15 * time.Second)
			os.Remove(tmpPath)
			for _, p := range tmpImages {
				os.Remove(p)
			}
		})
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
		htmlBody = render.SanitizeForBrowser(m.openHTMLBody)
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

// expectedMimePrefix maps common file extensions to expected MIME type prefixes.
// If magic-byte detection returns something outside the expected prefix, the file is suspicious.
var expectedMimePrefix = map[string]string{
	".png": "image/", ".jpg": "image/", ".jpeg": "image/", ".gif": "image/",
	".webp": "image/", ".bmp": "image/", ".ico": "image/",
	".pdf": "application/pdf",
	".zip": "application/zip", ".gz": "application/",
	".doc": "application/", ".docx": "application/", ".xls": "application/", ".xlsx": "application/",
	".mp3": "audio/", ".wav": "audio/", ".ogg": "audio/",
	".mp4": "video/", ".webm": "video/", ".avi": "video/",
}

// isMimeMismatch returns true if the file extension claims to be a safe type
// but magic-byte detection says otherwise (e.g. a script disguised as .png).
func isMimeMismatch(ext, detected string) bool {
	// SVG is XML-based, so DetectContentType returns text/xml, text/plain, or
	// text/html — all valid for real SVGs. Only flag binary content as suspicious.
	if ext == ".svg" {
		return !strings.HasPrefix(detected, "text/") && !strings.HasPrefix(detected, "image/")
	}
	expected, ok := expectedMimePrefix[ext]
	if !ok {
		return false // unknown extension — can't validate, let it through
	}
	return !strings.HasPrefix(detected, expected)
}

// dangerousExts lists file extensions that should not be auto-opened with xdg-open
// because they could execute arbitrary code.
var dangerousExts = map[string]bool{
	".sh": true, ".bash": true, ".zsh": true, ".fish": true,
	".exe": true, ".bat": true, ".cmd": true, ".com": true, ".scr": true, ".msi": true,
	".desktop": true, ".app": true, ".command": true, ".action": true,
	".py": true, ".rb": true, ".pl": true, ".ps1": true,
	".jar": true, ".class": true,
}

// downloadOpenAttachmentCmd saves the attachment to ~/Downloads and opens it
// with xdg-open (non-blocking — does not suspend the TUI).
// Dangerous file types are saved but NOT auto-opened.
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
		ext := strings.ToLower(filepath.Ext(base))
		if dangerousExts[ext] {
			return attachOpenDoneMsg{path: dst, dangerous: true, reason: fmt.Sprintf("executable extension %s", ext)}
		}
		// Magic-byte check: detect actual content type from file bytes.
		// Flags mismatches like a .sh disguised as .png (detected as text/plain, not image/png).
		if detected := http.DetectContentType(a.Data); isMimeMismatch(ext, detected) {
			// Extract just the MIME type without params for the message
			mimeType := detected
			if i := strings.IndexByte(mimeType, ';'); i >= 0 {
				mimeType = strings.TrimSpace(mimeType[:i])
			}
			return attachOpenDoneMsg{path: dst, dangerous: true, reason: fmt.Sprintf("content is %s, not %s", mimeType, ext)}
		}
		_ = exec.Command("xdg-open", dst).Start()
		return attachOpenDoneMsg{path: dst}
	}
}

// downloadEMLCmd fetches the raw MIME source and saves it as .eml to ~/Downloads.
func (m Model) downloadEMLCmd() tea.Cmd {
	e := m.openEmail
	if e == nil {
		return nil
	}
	cli := m.imapCli()
	folder := e.Folder
	uid := e.UID
	subject := e.Subject
	emailDate := e.Date
	return func() tea.Msg {
		raw, err := cli.FetchRaw(nil, folder, uid)
		if err != nil {
			return emlDownloadedMsg{err: err}
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return emlDownloadedMsg{err: err}
		}
		dir := filepath.Join(home, "Downloads")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return emlDownloadedMsg{err: fmt.Errorf("create Downloads: %w", err)}
		}
		// Sanitize subject for filename
		safe := strings.Map(func(r rune) rune {
			if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
				return '_'
			}
			return r
		}, subject)
		if len(safe) > 80 {
			safe = safe[:80]
		}
		if safe == "" {
			safe = "email"
		}
		datePart := emailDate.Format("20060102")
		base := fmt.Sprintf("neomd-%s-%s.eml", datePart, safe)
		dst := filepath.Join(dir, base)
		if _, err := os.Stat(dst); err == nil {
			for i := 1; ; i++ {
				dst = filepath.Join(dir, fmt.Sprintf("neomd-%s-%s_%d.eml", datePart, safe, i))
				if _, err := os.Stat(dst); os.IsNotExist(err) {
					break
				}
			}
		}
		if err := os.WriteFile(dst, raw, 0644); err != nil {
			return emlDownloadedMsg{err: fmt.Errorf("save EML: %w", err)}
		}
		return emlDownloadedMsg{path: dst}
	}
}

// rsvpDoneMsg is fired when an RSVP send completes.
type rsvpDoneMsg struct {
	status calendar.Status
	err    error
}

// icsOpenedMsg is fired after writing the .ics to ~/.cache and invoking
// the configured calendar open_command.
type icsOpenedMsg struct {
	path string
	err  error
}

// calendarInvite returns the first calendar invite attachment on the
// currently open email, or nil if none.
func (m Model) calendarInvite() *imap.Attachment {
	for i := range m.openAttachments {
		if m.openAttachments[i].IsCalendarInvite {
			return &m.openAttachments[i]
		}
	}
	return nil
}

// sendRSVPCmd builds an iMIP REPLY message and sends it to the event
// organizer. The responder address is the active account's user email.
func (m Model) sendRSVPCmd(status calendar.Status) tea.Cmd {
	att := m.calendarInvite()
	if att == nil {
		m.status = "No calendar invite to RSVP to."
		return nil
	}
	ev, err := calendar.Parse(att.Data)
	if err != nil {
		m.status = "RSVP parse: " + err.Error()
		m.isError = true
		return nil
	}
	if ev.Organizer == "" {
		m.status = "RSVP: invite has no ORGANIZER — cannot determine recipient."
		m.isError = true
		return nil
	}

	acct := m.activeAccount()
	responder := acct.User
	from := acct.From
	if from == "" {
		from = responder
	}

	reply, err := calendar.BuildRSVP(att.Data, responder, status)
	if err != nil {
		m.status = "RSVP build: " + err.Error()
		m.isError = true
		return nil
	}

	// Subject prefix matches what Gmail's own native "Yes / Maybe / No"
	// buttons emit: "Accepted: <event>", "Declined: <event>",
	// "Tentative: <event>". Gmail's iMIP processor uses this prefix as one
	// of its signals to match the REPLY to a calendar event; "Re: <event>"
	// gets treated as an ordinary reply and the calendar isn't updated.
	low := strings.ToLower(string(status))
	verb := strings.ToUpper(low[:1]) + low[1:]
	subject := verb + ": " + ev.Summary
	plain := fmt.Sprintf("%s: %s\n", verb, ev.Summary)
	if when := ev.FormatTime(); when != "" {
		plain += when
		if ev.Location != "" {
			plain += " at " + ev.Location
		}
		plain += "\n"
	}

	var inReplyTo, references string
	if m.openEmail != nil {
		inReplyTo = m.openEmail.MessageID
		references = m.openEmail.References
	}

	raw, err := smtp.BuildRSVPMessage(from, ev.Organizer, subject, plain, reply, inReplyTo, references)
	if err != nil {
		m.status = "RSVP MIME: " + err.Error()
		m.isError = true
		return nil
	}

	h, p := splitAddr(acct.SMTP)
	cfg := smtp.Config{
		Host: h, Port: p,
		User:        acct.User,
		Password:    acct.Password,
		From:        from,
		STARTTLS:    acct.STARTTLS,
		TLSCertFile: acct.TLSCertFile,
		TokenSource: m.tokenSourceFor(acct.Name),
	}

	to := ev.Organizer
	sentCli := m.sentDraftsIMAPClient()
	sentFolder := m.cfg.Folders.Sent
	openEmail := m.openEmail
	replyCli := m.imapCliForAccount(acct.Name)
	return func() tea.Msg {
		if err := smtp.SendRaw(cfg, []string{to}, raw); err != nil {
			return rsvpDoneMsg{status: status, err: err}
		}
		// Save a copy to Sent so the user has an audit trail of which
		// RSVPs they sent — regular replies save, RSVPs should too.
		// Non-fatal: if SaveSent fails the RSVP is already on the wire.
		if sentCli != nil && sentFolder != "" {
			_ = sentCli.SaveSent(nil, sentFolder, raw)
		}
		// Mark the original invite as \Answered so the inbox shows the
		// reply indicator (·), matching reply / forward behaviour.
		if openEmail != nil && replyCli != nil {
			_ = replyCli.MarkAnswered(nil, openEmail.Folder, openEmail.UID)
		}
		return rsvpDoneMsg{status: status}
	}
}

// openICSCmd writes the calendar invite to ~/.cache/neomd/ical/ and runs
// the configured open_command (default xdg-open). Lets the user import
// the event into their local calendar app (e.g. morgen, khal, GNOME).
func (m Model) openICSCmd() tea.Cmd {
	att := m.calendarInvite()
	if att == nil {
		m.status = "No calendar invite to open."
		return nil
	}
	// Split open_command on whitespace so configs like
	// `open_command = "khal import"` exec the binary `khal` with arg
	// `import`, not a literal binary named "khal import". Empty falls back
	// to xdg-open. Quoted multi-word paths are not supported here — users
	// who need that should symlink or wrap in a shell script.
	cmdParts := strings.Fields(strings.TrimSpace(m.cfg.Calendar.OpenCommand))
	if len(cmdParts) == 0 {
		cmdParts = []string{"xdg-open"}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		m.status = "open .ics: " + err.Error()
		m.isError = true
		return nil
	}
	dir := filepath.Join(home, ".cache", "neomd", "ical")
	if err := os.MkdirAll(dir, 0700); err != nil {
		m.status = "open .ics: " + err.Error()
		m.isError = true
		return nil
	}
	// Path traversal hardening: filepath.Base() strips any directory
	// components the sender may have stuck in Content-Disposition: filename
	// (e.g. "../../../.bashrc" → ".bashrc"). The result is then joined under
	// the cache dir so we can never write outside ~/.cache/neomd/ical/.
	name := filepath.Base(att.Filename)
	if name == "" || name == "." || name == "/" {
		name = fmt.Sprintf("invite-%d.ics", time.Now().Unix())
	}
	dst := filepath.Join(dir, name)
	if err := os.WriteFile(dst, att.Data, 0600); err != nil {
		m.status = "open .ics: " + err.Error()
		m.isError = true
		return nil
	}

	return func() tea.Msg {
		args := append(cmdParts[1:], dst)
		if err := exec.Command(cmdParts[0], args...).Start(); err != nil {
			return icsOpenedMsg{path: dst, err: err}
		}
		return icsOpenedMsg{path: dst}
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
	bcc := e.BCC
	from := e.From
	subject := e.Subject

	// Pre-fill compose fields so viewCompose shows them
	m.compose.reset()
	if idx := m.matchFromAddress(from); idx >= 0 {
		m.presendFromI = idx
	} else {
		m.presendFromI = 0
	}
	m.compose.to.SetValue(to)
	m.compose.cc.SetValue(cc)
	m.compose.bcc.SetValue(bcc)
	m.compose.subject.SetValue(subject)
	if cc != "" || bcc != "" {
		m.compose.extraVisible = true
	}
	m.compose.step = 3 // jump past header steps to subject-done state
	if len(m.openAttachments) > 0 {
		paths, err := writeAttachmentsTemp(m.openAttachments)
		if err != nil {
			m.status = "continueDraft attachments: " + err.Error()
			m.isError = true
			return m, nil
		}
		m.attachments = paths
	} else {
		m.attachments = nil
	}

	// Build temp file with prelude + existing body.
	// No signature — the draft body already contains it from the first compose.
	prelude := editor.Prelude(to, cc, bcc, m.presendFrom(), subject, "")
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
	m.status = ""
	m.isError = false
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
		pto, pcc, pbcc, pfrom, psubject, _ := editor.ParseHeaders(string(raw))
		if pto == "" {
			pto = to
		}
		if pcc == "" {
			pcc = cc
		}
		if pbcc == "" {
			pbcc = bcc
		}
		if pfrom == "" {
			pfrom = m.presendFrom()
		}
		if psubject == "" {
			psubject = subject
		}
		return editorDoneMsg{to: pto, cc: pcc, bcc: pbcc, from: pfrom, subject: psubject, body: string(raw)}
	})
}

func (m Model) updateCompose(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pendingDiscard {
		switch msg.String() {
		case "y":
			m.pendingDiscard = false
			m.attachments = nil
			m.pendingSend = nil
			m.state = stateInbox
			m.status = "Discarded. Use :recover to reopen the latest backup if needed."
			m.isError = false
			return m, nil
		case "n", "esc":
			m.cancelDiscardConfirm()
			return m, nil
		default:
			return m, nil
		}
	}

	switch msg.String() {
	case "esc":
		if m.hasComposeDraft() {
			m.beginDiscardConfirm()
			return m, nil
		}
		m.state = stateInbox
		m.status = "Cancelled."
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
		if len(froms) <= 1 {
			m.status = "Only one From address configured. Add another account or [[senders]] alias to cycle."
			return m, nil
		}
		m.presendFromI = (m.presendFromI + 1) % len(froms)
		return m, nil
	}
	if m.status != "" {
		m.status = ""
		m.isError = false
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
	if m.pendingDiscard {
		switch msg.String() {
		case "y":
			m.pendingDiscard = false
			m.attachments = nil
			m.pendingSend = nil
			m.state = stateInbox
			m.status = "Discarded. Use :recover to reopen the latest backup if needed."
			m.isError = false
			return m, nil
		case "n", "esc":
			m.cancelDiscardConfirm()
			return m, nil
		default:
			return m, nil
		}
	}
	// AI prompt input mode: typed when user pressed `i`. Enter submits
	// (empty → interactive, non-empty → templated into [ai].args). Esc
	// cancels back to the normal pre-send screen.
	if m.aiPromptActive {
		switch msg.String() {
		case "esc":
			m.aiPromptActive = false
			return m, nil
		case "enter":
			prompt := strings.TrimSpace(m.aiPromptInput.Value())
			m.aiPromptActive = false
			return m.launchAIHandoffCmd(ps, prompt)
		}
		var cmd tea.Cmd
		m.aiPromptInput, cmd = m.aiPromptInput.Update(msg)
		return m, cmd
	}
	switch msg.String() {
	case "enter":
		m.loading = true
		m.state = stateInbox
		from := m.presendFrom()
		smtpAcct := m.presendSMTPAccount()
		attachments := m.attachments
		replyUID, replyFolder := ps.replyToUID, ps.replyToFolder
		// Extract [html-signature] marker from body now (right before sending)
		includeHTMLSig, cleanBody := extractHTMLSignatureMarker(ps.body)
		m.attachments = nil
		m.pendingSend = nil
		// Route to Listmonk if the To address matches a configured trigger.
		if m.cfg.ListmonkEnabled() {
			if listIDs := listmonk.ResolveListIDs(m.listmonkTriggers(), ps.to); len(listIDs) > 0 {
				return m, tea.Batch(m.spinner.Tick, m.sendListmonkCmd(ps.subject, cleanBody, listIDs))
			}
		}
		return m, tea.Batch(m.spinner.Tick, m.sendEmailCmd(smtpAcct, from, ps.to, ps.cc, ps.bcc, ps.subject, cleanBody, attachments, includeHTMLSig, replyUID, replyFolder, ps.replyToAccount, ps.inReplyTo, ps.references))
	case "ctrl+f":
		froms := m.presendFroms()
		if len(froms) <= 1 {
			m.status = "Only one From address configured. Add another account or [[senders]] alias to cycle."
			return m, nil
		}
		m.presendFromI = (m.presendFromI + 1) % len(froms)
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
	case "i":
		// AI handoff: prompt for a one-line instruction, then spawn
		// [ai].command. Empty input → interactive mode (no {prompt}
		// substitution); a typed instruction (e.g. "fix grammar") gets
		// templated into [ai].args via {prompt} so e.g. claude can run
		// non-interactively as `claude -p "fix grammar" <draft>`.
		if strings.TrimSpace(m.cfg.AI.Command) == "" {
			m.status = "AI handoff: set [ai].command in config.toml (e.g. command = \"claude\")"
			return m, nil
		}
		ti := textinput.New()
		ti.Placeholder = "fix grammar  (Enter to run · empty = interactive · esc to cancel)"
		ti.CharLimit = 200
		ti.Focus()
		m.aiPromptInput = ti
		m.aiPromptActive = true
		return m, nil
	case "d":
		// Save to Drafts without sending.
		return m, m.saveDraftCmd(m.presendIMAPClient(), m.presendFrom(), ps.to, ps.cc, ps.bcc, ps.subject, ps.body, m.attachments)
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
		m.beginDiscardConfirm()
		return m, nil
	case "p":
		return m.previewInBrowser()
	case "esc":
		m.beginDiscardConfirm()
		return m, nil
	}
	if m.status != "" {
		m.status = ""
		m.isError = false
	}
	return m, nil
}

// launchSpellCheckCmd opens the composed email body in nvim with spell checking
// enabled and the cursor positioned on the first misspelled word.
// On return, the (possibly corrected) body replaces the pre-send body.
func (m Model) launchSpellCheckCmd(ps *pendingSendData) (tea.Model, tea.Cmd) {
	prelude := editor.Prelude(ps.to, ps.cc, ps.bcc, m.presendFrom(), ps.subject, "")
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
		pto, pcc, pbcc, pfrom, psubject, _ := editor.ParseHeaders(string(raw))
		if pto == "" {
			pto = ps.to
		}
		if pcc == "" {
			pcc = ps.cc
		}
		if pbcc == "" {
			pbcc = ps.bcc
		}
		if pfrom == "" {
			pfrom = m.presendFrom()
		}
		if psubject == "" {
			psubject = ps.subject
		}
		return editorDoneMsg{to: pto, cc: pcc, bcc: pbcc, from: pfrom, subject: psubject, body: string(raw)}
	})
}

// launchAIHandoffCmd writes the current draft to a temp markdown file and
// spawns [ai].command against it. Same temp-file round-trip as the editor and
// spell-check flows: on exit, the file is re-parsed and the draft body is
// replaced. The configured command receives the file path as its final
// argument so e.g. `claude` or `codex` work the same as `nvim`.
//
// `prompt` is the optional one-line instruction the user typed after `i`.
// Any `{prompt}` placeholder in [ai].args is replaced by it so non-interactive
// CLIs can run directly, e.g. with [ai].args = ["-p", "{prompt}"] and
// prompt = "fix grammar" the spawn becomes `claude -p "fix grammar" <file>`.
// Empty prompt → no substitution → interactive mode (current behaviour).
func (m Model) launchAIHandoffCmd(ps *pendingSendData, prompt string) (tea.Model, tea.Cmd) {
	prelude := editor.Prelude(ps.to, ps.cc, ps.bcc, m.presendFrom(), ps.subject, "")
	content := prelude + ps.body

	f, err := os.CreateTemp(neomdTempDir(), "neomd-ai-*.md")
	if err != nil {
		m.status = "AI handoff: " + err.Error()
		m.isError = true
		return m, nil
	}
	tmpPath := f.Name()
	f.WriteString(content) //nolint
	f.Close()

	// Expand {prompt} and {file} placeholders. {file} is the basename only
	// (claude/codex/aichat take files via prompt mention, not positional);
	// the directory containing the file becomes the spawned command's cwd
	// just below so the AI tool's built-in file-edit tool can reach it
	// without --add-dir or absolute paths.
	filename := filepath.Base(tmpPath)
	args := make([]string, 0, len(m.cfg.AI.Args))
	for _, a := range m.cfg.AI.Args {
		hadPromptPlaceholder := strings.Contains(a, "{prompt}")
		s := strings.ReplaceAll(a, "{prompt}", prompt)
		s = strings.ReplaceAll(s, "{file}", filename)
		// Empty input + arg that was just `{prompt}` → drop the arg so the
		// spawn becomes `claude` rather than `claude ""`. (An arg that
		// contained both `{prompt}` and `{file}`, like the default, is
		// non-empty after {file} expansion and survives.)
		if hadPromptPlaceholder && s == "" {
			continue
		}
		args = append(args, s)
	}
	cmd := exec.Command(m.cfg.AI.Command, args...)
	cmd.Dir = filepath.Dir(tmpPath)
	m.state = stateCompose
	draftBackups := m.cfg.UI.DraftBackups()
	return m, tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		backupDraft(tmpPath, draftBackups)
		defer os.Remove(tmpPath)
		// Read the temp file regardless of exit code. AI CLIs typically exit
		// non-zero on Ctrl+C (130 = SIGINT), and that's the documented "quit
		// to return" path — treating it as an error would clear attachments
		// and bounce the user back to the inbox via editorDoneMsg{err}. The
		// file we wrote at launch is still there even if the tool exited
		// cleanly without touching it; parsing then yields the original draft.
		raw, readErr := os.ReadFile(tmpPath)
		if readErr != nil {
			// Surface the exec error too if both happened — useful for
			// diagnosing missing-binary cases ([ai].command typo).
			err := readErr
			if execErr != nil {
				err = fmt.Errorf("%w (ai exec: %v)", readErr, execErr)
			}
			return editorDoneMsg{err: err}
		}
		pto, pcc, pbcc, pfrom, psubject, _ := editor.ParseHeaders(string(raw))
		if pto == "" {
			pto = ps.to
		}
		if pcc == "" {
			pcc = ps.cc
		}
		if pbcc == "" {
			pbcc = ps.bcc
		}
		if pfrom == "" {
			pfrom = m.presendFrom()
		}
		if psubject == "" {
			psubject = ps.subject
		}
		return editorDoneMsg{to: pto, cc: pcc, bcc: pbcc, from: pfrom, subject: psubject, body: string(raw)}
	})
}

// previewInBrowser renders the composed email as HTML (same pipeline as sending)
// and opens it in $BROWSER so the user can verify images and formatting.
func (m Model) previewInBrowser() (tea.Model, tea.Cmd) {
	ps := m.pendingSend
	if ps == nil {
		return m, nil
	}

	// Extract [html-signature] marker the same way as the send path
	// so the preview matches what recipients will actually receive
	includeHTMLSig, cleanBody := extractHTMLSignatureMarker(ps.body)

	htmlBody, err := render.ToHTML(cleanBody)
	if err != nil {
		m.status = "preview: " + err.Error()
		m.isError = true
		return m, nil
	}

	// Inject HTML signature before </body> tag if enabled (matching send path)
	if includeHTMLSig {
		htmlSig := m.cfg.UI.HTMLSignature()
		if htmlSig != "" {
			idx := strings.LastIndex(htmlBody, "</body>")
			if idx >= 0 {
				htmlBody = htmlBody[:idx] + "\n" + htmlSig + "\n" + htmlBody[idx:]
			}
		}
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
		safeGo(func() {
			time.Sleep(15 * time.Second)
			os.Remove(tmpPath)
		})
		return nil
	}
}

func (m Model) saveDraftCmd(imapCli *imap.Client, from, to, cc, bcc, subject, body string, attachments []string) tea.Cmd {
	folder := m.cfg.Folders.Drafts
	return func() tea.Msg {
		raw, err := smtp.BuildDraftMessage(from, to, cc, bcc, subject, body, attachments)
		if err != nil {
			return saveDraftDoneMsg{err: err}
		}
		err = imapCli.SaveDraft(nil, folder, raw)
		return saveDraftDoneMsg{err: err}
	}
}

func (m Model) launchEditorCmd() (tea.Model, tea.Cmd) {
	to := m.compose.to.Value()
	cc := m.compose.cc.Value()
	bcc := m.compose.bcc.Value()
	subject := m.compose.subject.Value()
	// Consume any mailto body (pre-filled from --mailto flag).
	mailtoBody := m.mailtoBody
	m.mailtoBody = ""

	// When a mailto body is present, insert it before the signature so
	// the signature always appears at the bottom of the composed message.
	sig := m.cfg.UI.TextSignature()
	var prelude string
	if mailtoBody != "" {
		// Build headers without signature, append body, then signature.
		prelude = editor.Prelude(to, cc, bcc, m.presendFrom(), subject, "")
		prelude += mailtoBody
		if sig != "" {
			prelude += "\n\n--  \n" + sig + "\n"
		}
	} else {
		prelude = editor.Prelude(to, cc, bcc, m.presendFrom(), subject, sig)
	}

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
		pto, pcc, pbcc, pfrom, psubject, _ := editor.ParseHeaders(string(raw))
		if pto == "" {
			pto = to
		}
		if pcc == "" {
			pcc = cc
		}
		if pbcc == "" {
			pbcc = bcc
		}
		if pfrom == "" {
			pfrom = m.presendFrom()
		}
		if psubject == "" {
			psubject = subject
		}
		return editorDoneMsg{to: pto, cc: pcc, bcc: pbcc, from: pfrom, subject: psubject, body: string(raw)}
	})
}

// launchEditorWithBodyCmd re-opens the editor with an existing body (e.g. from
// the pre-send screen). The prelude is built from the provided headers (no
// signature — it is already in the body from the first compose).
func (m Model) launchEditorWithBodyCmd(to, cc, bcc, subject, body string) (tea.Model, tea.Cmd) {
	prelude := editor.Prelude(to, cc, bcc, m.presendFrom(), subject, "")
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
		pto, pcc, pbcc, pfrom, psubject, _ := editor.ParseHeaders(string(raw))
		if pto == "" {
			pto = to
		}
		if pcc == "" {
			pcc = cc
		}
		if pbcc == "" {
			pbcc = bcc
		}
		if pfrom == "" {
			pfrom = m.presendFrom()
		}
		if psubject == "" {
			psubject = subject
		}
		return editorDoneMsg{to: pto, cc: pcc, bcc: pbcc, from: pfrom, subject: psubject, body: string(raw)}
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

func (m Model) enterReactionMode(e *imap.Email) (tea.Model, tea.Cmd) {
	m.prevState = m.state
	m.state = stateReaction
	m.reactionEmail = e
	m.reactionSelected = 0
	m.pendingReaction = false

	// Pre-select the correct From address (same logic as reply)
	if idx := m.matchFromIndex(e.To, e.CC); idx >= 0 {
		m.presendFromI = idx
	}

	return m, nil
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
		pto, _, _, pfrom, psubject, _ := editor.ParseHeaders(string(raw))
		if pfrom == "" {
			pfrom = m.presendFrom()
		}
		if psubject == "" {
			if !strings.HasPrefix(strings.ToLower(subject), "fwd:") {
				psubject = "Fwd: " + subject
			} else {
				psubject = subject
			}
		}
		return editorDoneMsg{to: pto, cc: "", bcc: "", from: pfrom, subject: psubject, body: string(raw)}
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
		// Collect original To + CC, exclude all own addresses.
		// Build exclusion set from both account User (IMAP login) and From (send-as)
		// to handle setups where they differ (e.g., user123@provider vs simon@domain).
		ownAddrs := make(map[string]bool)
		// Add all account User addresses (IMAP login)
		for _, acc := range m.accounts {
			ownAddrs[strings.ToLower(extractEmailAddr(acc.User))] = true
		}
		// Add all From addresses (accounts + sender aliases)
		for _, from := range m.presendFroms() {
			ownAddrs[strings.ToLower(extractEmailAddr(from))] = true
		}
		var parts []string
		for _, addr := range splitAddrs(e.To + "," + e.CC) {
			if a := strings.TrimSpace(addr); a != "" {
				addrLower := strings.ToLower(extractEmailAddr(a))
				if !ownAddrs[addrLower] {
					parts = append(parts, a)
				}
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
		pto, pcc, _, pfrom, psubject, _ := editor.ParseHeaders(string(raw))
		if pto == "" {
			pto = to
		}
		if pcc == "" {
			pcc = cc
		}
		if pfrom == "" {
			pfrom = m.presendFrom()
		}
		if psubject == "" {
			psubject = subject
		}
		return editorDoneMsg{to: pto, cc: pcc, bcc: "", from: pfrom, subject: psubject, body: string(raw)}
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

func (m Model) matchFromAddress(from string) int {
	target := strings.ToLower(extractEmailAddr(from))
	if target == "" {
		return -1
	}
	for i, candidate := range m.presendFroms() {
		if strings.ToLower(extractEmailAddr(candidate)) == target {
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

// domainEntry returns the lowercased "@domain.tld" form of from for use as a
// screener list entry, or an empty string if the address has no '@' part.
func domainEntry(from string) string {
	addr := strings.ToLower(extractEmailAddr(from))
	if i := strings.IndexByte(addr, '@'); i >= 0 {
		return addr[i:]
	}
	return ""
}

// extractName extracts the name part from "Name <email@example.com>" format.
// Returns empty string if there's no name part.
func extractName(s string) string {
	if i := strings.IndexByte(s, '<'); i >= 0 {
		name := strings.TrimSpace(s[:i])
		// Remove quotes if present
		name = strings.Trim(name, "\"")
		return name
	}
	return ""
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

// extractHTMLSignatureMarker scans body for [html-signature] marker.
// If found, removes it and returns (true, cleanBody).
// If not found, returns (false, body unchanged).
func extractHTMLSignatureMarker(body string) (includeHTMLSig bool, clean string) {
	const marker = "[html-signature]"
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == marker {
			includeHTMLSig = true
			continue
		}
		kept = append(kept, line)
	}
	return includeHTMLSig, strings.Join(kept, "\n")
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
	case stateReaction:
		return m.viewReaction()
	}
	return ""
}

func (m Model) viewPresend() string {
	ps := m.pendingSend
	if ps == nil {
		return ""
	}
	isListmonk := m.cfg.ListmonkEnabled() && listmonk.IsTriggerAddress(m.listmonkTriggers(), ps.to)
	var b strings.Builder
	if isListmonk {
		b.WriteString(styleHeader.Render("  Newsletter via Listmonk") + "\n")
		b.WriteString(styleSeparator.Render(strings.Repeat("─", m.width)) + "\n")
		listIDs := listmonk.ResolveListIDs(m.listmonkTriggers(), ps.to)
		delay := m.cfg.Listmonk.DelayMinutes
		if delay == 0 {
			delay = 30
		}
		b.WriteString(styleHelp.Render(fmt.Sprintf("  Lists: %v · Schedule: in %d min", listIDs, delay)) + "\n\n")
	} else {
		b.WriteString(styleHeader.Render("  Ready to send") + "\n")
		b.WriteString(styleSeparator.Render(strings.Repeat("─", m.width)) + "\n\n")
	}

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
	if m.aiPromptActive {
		// One-line instruction prompt active — replaces the help footer
		// until the user submits (Enter) or cancels (Esc).
		b.WriteString(styleInputLabel.Render("AI prompt:") + " " + m.aiPromptInput.View())
	} else if m.status != "" {
		b.WriteString(statusBar(m.status, m.isError))
	} else {
		if isListmonk {
			b.WriteString(styleHelp.Render("  enter schedule campaign · e edit · p preview · ctrl+f from · d draft · esc cancel · x discard"))
		} else {
			aiHint := ""
			if strings.TrimSpace(m.cfg.AI.Command) != "" {
				aiHint = " · i AI (quit to return)"
			}
			b.WriteString(styleHelp.Render("  enter send · e edit · s spell · p preview · a attach · D remove attach" + aiHint + " · ctrl+f from · ctrl+b cc/bcc · d draft · esc cancel · x discard"))
		}
	}
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
		if len(m.emails) > 0 {
			help += styleDate.Render(fmt.Sprintf("  │  %d loaded", len(m.emails)))
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
	if m.status != "" {
		b.WriteString("\n" + statusBar(m.status, m.isError))
	} else {
		b.WriteString("\n" + readerHelp(isDraft, len(m.openLinks) > 0))
	}
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
	if m.status != "" {
		b.WriteString(statusBar(m.status, m.isError))
	} else {
		b.WriteString(composeHelp(int(m.compose.step), len(m.presendFroms()) > 1))
	}
	return b.String()
}

func (m Model) updateReaction(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.state = m.prevState
		m.reactionEmail = nil
		return m, nil

	case "j", "down":
		if m.reactionSelected < len(defaultReactions)-1 {
			m.reactionSelected++
		}

	case "k", "up":
		if m.reactionSelected > 0 {
			m.reactionSelected--
		}

	case "1", "2", "3", "4", "5", "6", "7", "8":
		idx, _ := strconv.Atoi(msg.String())
		if idx >= 1 && idx <= len(defaultReactions) {
			return m.sendReaction(idx - 1)
		}

	case "enter":
		return m.sendReaction(m.reactionSelected)
	}

	return m, nil
}

func (m Model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.helpSearchActive {
			if m.helpSearch != "" {
				m.helpSearch = ""
				m.helpScroll = 0
			} else {
				m.helpSearchActive = false
			}
		} else if m.helpSearch != "" {
			m.helpSearch = ""
			m.helpScroll = 0
		} else {
			m.state = m.prevState
		}
	case "enter":
		if m.helpSearchActive {
			m.helpSearchActive = false
		}
	case "q":
		if !m.helpSearchActive {
			m.state = m.prevState
		} else {
			m.helpSearch += "q"
		}
	case "backspace", "ctrl+h":
		if m.helpSearchActive && len(m.helpSearch) > 0 {
			runes := []rune(m.helpSearch)
			m.helpSearch = string(runes[:len(runes)-1])
			m.helpScroll = 0
		}
	case "/":
		if m.helpSearchActive {
			m.helpSearch += "/"
			m.helpScroll = 0
		} else {
			m.helpSearchActive = true
		}
	case "j", "down":
		if m.helpSearchActive {
			m.helpSearch += "j"
			m.helpScroll = 0
		} else {
			m.helpScroll++
		}
	case "k", "up":
		if m.helpSearchActive {
			m.helpSearch += "k"
			m.helpScroll = 0
		} else if m.helpScroll > 0 {
			m.helpScroll--
		}
	case "d":
		if m.helpSearchActive {
			m.helpSearch += "d"
			m.helpScroll = 0
		} else {
			m.helpScroll += m.helpPageSize()
		}
	case "ctrl+d":
		if !m.helpSearchActive {
			m.helpScroll += m.helpPageSize()
		}
	case "u":
		if m.helpSearchActive {
			m.helpSearch += "u"
			m.helpScroll = 0
		} else {
			m.helpScroll -= m.helpPageSize()
		}
	case "ctrl+u":
		if !m.helpSearchActive {
			m.helpScroll -= m.helpPageSize()
		}
	default:
		if m.helpSearchActive && len(msg.String()) == 1 {
			m.helpSearch += msg.String()
			m.helpScroll = 0
		}
	}
	m.clampHelpScroll()
	return m, nil
}

func (m Model) viewHelp() string {
	keyStyle := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true).Width(24)
	titleStyle := lipgloss.NewStyle().Foreground(colorDateCol).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(colorText)
	matchStyle := lipgloss.NewStyle().Foreground(colorAuthorUnread).Bold(true)

	filter := strings.ToLower(m.helpSearch)

	// Build header with logo on the right (only if at top of scroll)
	var headerLines []string
	if m.helpScroll == 0 {
		heading := styleHeader.Render("  Keyboard shortcuts")
		logo := asciiLogoCompact(colorPrimary)
		logoLines := strings.Split(strings.TrimPrefix(logo, "\n"), "\n")

		// Shorten separator to fit left column only
		leftColWidth := 70
		sep := styleSeparator.Render(strings.Repeat("─", leftColWidth))

		// Create header rows with logo on the right
		leftStyle := lipgloss.NewStyle().Width(leftColWidth).Align(lipgloss.Left)

		// First line: heading + logo line 1
		headerLines = append(headerLines, lipgloss.JoinHorizontal(lipgloss.Top,
			leftStyle.Render(heading),
			logoLines[0]))

		// Second line: separator + logo line 2
		headerLines = append(headerLines, lipgloss.JoinHorizontal(lipgloss.Top,
			leftStyle.Render(sep),
			logoLines[1]))

		// Remaining logo lines with empty left side
		for i := 2; i < len(logoLines); i++ {
			headerLines = append(headerLines, lipgloss.JoinHorizontal(lipgloss.Top,
				leftStyle.Render(""),
				logoLines[i]))
		}
	} else {
		// When scrolled, just show normal header
		heading := styleHeader.Render("  Keyboard shortcuts")
		sep := styleSeparator.Render(strings.Repeat("─", m.width))
		headerLines = []string{heading, sep}
	}

	lines := headerLines
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
		lines = append(lines, "", titleStyle.Render("  "+sec.Title))
		for _, row := range matched {
			lines = append(lines, "  "+keyStyle.Render(row[0])+descStyle.Render(row[1]))
		}
	}

	var searchLine string
	if m.helpSearchActive {
		searchLine = matchStyle.Render("  /"+m.helpSearch+"█") + styleHelp.Render("  · enter done · esc clear")
	} else if filter != "" {
		searchLine = matchStyle.Render("  /"+m.helpSearch) + styleHelp.Render("  · j/k scroll · / edit filter · esc clear")
	} else {
		searchLine = styleHelp.Render("  j/k scroll · d/u page · / filter · ? or q close")
	}

	contentHeight := m.height - 1
	if contentHeight < 1 {
		contentHeight = len(lines)
	}
	start := m.helpScroll
	if start < 0 {
		start = 0
	}
	maxStart := len(lines) - contentHeight
	if maxStart < 0 {
		maxStart = 0
	}
	if start > maxStart {
		start = maxStart
	}
	end := start + contentHeight
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	for _, line := range lines[start:end] {
		b.WriteString(line + "\n")
	}
	b.WriteString(searchLine)
	return b.String()
}

func (m Model) helpPageSize() int {
	if m.height <= 8 {
		return 1
	}
	return (m.height - 4) / 2
}

func (m Model) helpContentLineCount() int {
	filter := strings.ToLower(m.helpSearch)
	count := 2
	for _, sec := range HelpSections {
		matched := 0
		for _, row := range sec.Rows {
			if filter == "" || strings.Contains(strings.ToLower(row[0]), filter) || strings.Contains(strings.ToLower(row[1]), filter) {
				matched++
			}
		}
		if matched == 0 {
			continue
		}
		count += 2 + matched
	}
	return count
}

func (m *Model) clampHelpScroll() {
	if m.helpScroll < 0 {
		m.helpScroll = 0
	}
	contentHeight := m.height - 1
	if contentHeight < 1 {
		contentHeight = 1
	}
	maxScroll := m.helpContentLineCount() - contentHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.helpScroll > maxScroll {
		m.helpScroll = maxScroll
	}
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
