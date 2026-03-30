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
	sendDoneMsg       struct{ err error }
	screenDoneMsg     struct{ err error }
	autoScreenDoneMsg   struct{ moved int; err error }
	deepScreenReadyMsg  struct {
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
	deleteAllReadyMsg struct{ uids []uint32; folder string }
	// ensureFoldersDoneMsg reports which folders were created.
	ensureFoldersDoneMsg struct{ created []string; err error }
	moveDoneMsg       struct{ err error; undo []undoMove }
	batchDoneMsg      struct{ err error; undo []undoMove }
	undoDoneMsg       struct{}
	toggleSeenDoneMsg struct{ uid uint32; seen bool; err error }
	errMsg            struct{ err error }
	// background sync (runs every bgSyncInterval while neomd is open)
	bgSyncTickMsg     struct{}
	bgInboxFetchedMsg struct{ emails []imap.Email }
	bgScreenDoneMsg   struct{ moved int }
	// attachPickDoneMsg carries paths selected via the file picker (yazi etc.)
	attachPickDoneMsg struct{ paths []string }
	saveDraftDoneMsg         struct{ err error }
	attachOpenDoneMsg        struct{ path string; err error }
	editorDoneMsg     struct {
		to, cc, bcc, subject, body string
		err                        error
		aborted                    bool // true when file was unchanged (ZQ / :q!)
	}
)

// pendingSendData holds a composed message waiting in the pre-send review screen.
type pendingSendData struct {
	to, cc, bcc, subject, body string
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

	// Folder switcher
	folders       []string
	activeFolderI int
	offTabFolder  string // non-empty when viewing a folder not in the tab bar (e.g. "Spam", "Drafts")

	// Inbox
	inbox   list.Model
	emails  []imap.Email
	spinner spinner.Model

	// Reader
	reader    viewport.Model
	openEmail       *imap.Email
	openBody        string          // markdown body used by the TUI reader
	openHTMLBody    string          // original HTML part; used by openInExternalViewer when available
	openWebURL      string          // canonical "view online" URL for ctrl+o (may be empty)
	openAttachments []imap.Attachment // attachments of the currently open email

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

	// Forward: when true, bodyLoadedMsg launches forward editor instead of reader
	pendingForward bool

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

	return Model{
		cfg:         cfg,
		accounts:    cfg.ActiveAccounts(),
		clients:     clients,
		screener:    sc,
		state:       stateInbox,
		loading:     true,
		folders:     cfg.Folders.TabLabels(),
		cmdHistory:  loadCmdHistory(config.HistoryPath()),
		cmdHistI:    -1,
		// Note: Spam is intentionally excluded from tabs — use :go-spam to visit.
		compose:     newComposeModel(),
		spinner:     sp,
		markedUIDs:  make(map[uint32]bool),
		sortField:   "date",
		sortReverse: true, // newest first
	}
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

func (m Model) sendEmailCmd(smtpAcct config.AccountConfig, from, to, cc, bcc, subject, body string, attachments []string) tea.Cmd {
	h, p := splitAddr(smtpAcct.SMTP)
	cfg := smtp.Config{
		Host:     h,
		Port:     p,
		User:     smtpAcct.User,
		Password: smtpAcct.Password,
		From:     from,
	}
	cli := m.imapCli()
	sentFolder := m.cfg.Folders.Sent
	return func() tea.Msg {
		// Build raw MIME once — reused for both SMTP delivery and Sent copy.
		// BCC is intentionally excluded from headers but included in RCPT TO.
		raw, err := smtp.BuildMessage(from, to, cc, subject, body, attachments)
		if err != nil {
			return sendDoneMsg{fmt.Errorf("build message: %w", err)}
		}
		toAddrs := collectRcptTo(to, cc, bcc)
		if err := smtp.SendRaw(cfg, toAddrs, raw); err != nil {
			return sendDoneMsg{err}
		}
		// Save copy to Sent; non-fatal if it fails.
		if saveErr := cli.SaveSent(nil, sentFolder, raw); saveErr != nil {
			// Log to status on next tick — for now swallow so send still reports success.
			_ = saveErr
		}
		return sendDoneMsg{nil}
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
	type mv struct{ folder string; uid uint32 }
	moves := make([]mv, len(emails))
	for i, e := range emails {
		moves[i] = mv{e.Folder, e.UID}
	}
	return func() tea.Msg {
		undos := make([]undoMove, 0, len(moves))
		for i, mv := range moves {
			destUID, err := m.imapCli().MoveMessage(nil, mv.folder, mv.uid, dst)
			if err != nil {
				return batchDoneMsg{err: fmt.Errorf("stopped after %d/%d: %w", i, len(moves), err)}
			}
			undos = append(undos, undoMove{uid: destUID, fromFolder: mv.folder, toFolder: dst})
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
	type op struct{ from, srcFolder string; uid uint32; dst string }
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
	return func() tea.Msg {
		for i, o := range ops {
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
			if o.dst != "" && o.dst != o.srcFolder {
				if _, err := m.imapCli().MoveMessage(nil, o.srcFolder, o.uid, o.dst); err != nil {
					return batchDoneMsg{err: fmt.Errorf("stopped after %d/%d: %w", i, len(ops), err)}
				}
			}
		}
		return batchDoneMsg{}
	}
}

// markAllSeenCmd marks every currently loaded email in the folder as \Seen.
func (m Model) markAllSeenCmd() tea.Cmd {
	type op struct{ folder string; uid uint32 }
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
	type op struct{ folder string; uid uint32; markSeen bool }
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
	return func() tea.Msg {
		moved := 0
		for _, mv := range moves {
			if _, err := m.imapCli().MoveMessage(nil, src, mv.email.UID, mv.dst); err != nil {
				break
			}
			moved++
		}
		return bgScreenDoneMsg{moved: moved}
	}
}

// execAutoScreenCmd performs the IMAP moves for a pre-approved list of moves.
func (m Model) execAutoScreenCmd(moves []autoScreenMove) tea.Cmd {
	src := m.cfg.Folders.Inbox
	return func() tea.Msg {
		for i, mv := range moves {
			if _, err := m.imapCli().MoveMessage(nil, src, mv.email.UID, mv.dst); err != nil {
				return autoScreenDoneMsg{moved: i, err: err}
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

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		listH := msg.Height - 4
		if listH < 5 {
			listH = 5
		}
		if m.inbox.Width() == 0 {
			m.inbox = newInboxList(msg.Width, listH)
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
		if msg.folder == m.cfg.Folders.Inbox && m.cfg.UI.AutoScreen() {
			if moves := m.previewAutoScreen(); len(moves) > 0 {
				m.loading = true
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
		_ = loadEmailIntoReader(&m.reader, msg.email, msg.body, msg.attachments, m.cfg.UI.Theme, m.width)
		m.state = stateReading
		return m, nil

	case sendDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.status = msg.err.Error()
			m.isError = true
		} else {
			m.status = "Sent!"
			m.isError = false
			m.state = stateInbox
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
		return m, setEmails(&m.inbox, m.emails, m.markedUIDs)

	case batchDoneMsg:
		m.loading = false
		m.markedUIDs = make(map[uint32]bool)
		if msg.err != nil {
			m.status = msg.err.Error()
			m.isError = true
			return m, nil
		}
		if len(msg.undo) > 0 {
			m.undoStack = append(m.undoStack, msg.undo)
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
		if msg.err != nil {
			m.status = msg.err.Error()
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
			to: msg.to, cc: msg.cc, bcc: msg.bcc,
			subject: msg.subject, body: cleanBody,
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

	// ── Chord prefixes ──────────────────────────────────────────────
	case "g":
		m.pendingKey = "g"
		m.status = "go to:  gi inbox  ga archive  gf feed  gp papertrail  gt trash  gs sent  gk toscreen  go screened-out  gw waiting  gm someday  gd drafts  gS spam  gg top"
		return m, nil

	case " ": // leader key — wait for digit or shortcut
		m.pendingKey = " "
		m.status = "leader:  1-9 folder tab  (press digit, esc to cancel)"
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

	case "U": // clear all marks
		m.markedUIDs = make(map[uint32]bool)
		return m, setEmails(&m.inbox, m.emails, m.markedUIDs)

	case "u": // undo last move/delete
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
		return m, tea.Batch(m.spinner.Tick, m.batchScreenerCmd(targets, key))

	// A = archive (pure move, no screener update)
	case "A":
		targets := m.targetEmails()
		if len(targets) == 0 {
			return m, nil
		}
		m.loading = true
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
	case "tab", "L":
		m.activeFolderI = (m.activeFolderI + 1) % len(m.folders)
		m.offTabFolder = ""
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))

	case "shift+tab", "H":
		m.activeFolderI = (m.activeFolderI - 1 + len(m.folders)) % len(m.folders)
		m.offTabFolder = ""
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

	case "f":
		e := selectedEmail(m.inbox)
		if e == nil {
			return m, nil
		}
		m.pendingForward = true
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchBodyCmd(e))

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
		return m, setEmails(&m.inbox, m.emails, m.markedUIDs)

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
	return setEmails(&m.inbox, m.emails, m.markedUIDs)
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
		if len(key) == 1 && key >= "1" && key <= "9" {
			idx := int(key[0]-'1') // 0-based
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
		folderMap := map[string]string{
			"i": "Inbox",
			"f": "Feed",
			"p": "PaperTrail",
			"t": "Trash",
			"s": "Sent",
			"k": "ToScreen",
			"a": "Archive",
			"w": "Waiting",
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
			"m": m.cfg.Folders.Someday,
		}
		if dst, ok := dstMap[key]; ok {
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.batchMoveCmd(targets, dst))
		}
		m.status = fmt.Sprintf("unknown: M%s", key)

	case ",":
		type sortSpec struct{ field string; rev bool }
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
	switch msg.String() {
	case "q", "esc", "h":
		m.state = stateInbox
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
		if m.openEmail != nil {
			return m.launchReplyAllCmd()
		}
	case "f":
		if m.openEmail != nil {
			return m.launchForwardCmd()
		}
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx := int(msg.String()[0]-'1') // 0-based
		if idx < len(m.openAttachments) {
			return m, m.downloadOpenAttachmentCmd(m.openAttachments[idx])
		}
	}
	var cmd tea.Cmd
	m.reader, cmd = m.reader.Update(msg)
	return m, cmd
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

	f, err := os.CreateTemp("", "neomd-view-*.html")
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

	f, err := os.CreateTemp("", "neomd-view-*.html")
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

	f, err := os.CreateTemp("", "neomd-*.md")
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
	m.state = stateCompose
	return m, tea.ExecProcess(cmd, func(execErr error) tea.Msg {
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
		m.attachments = nil
		m.pendingSend = nil
		return m, tea.Batch(m.spinner.Tick, m.sendEmailCmd(smtpAcct, from, ps.to, ps.cc, ps.bcc, ps.subject, ps.body, attachments))
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
		// Build a temp file with existing body and re-launch.
		m.state = stateCompose
		m.pendingSend = nil
		prelude := editor.Prelude(ps.to, ps.cc, ps.subject, m.cfg.UI.Signature)
		// Pre-fill compose fields so launchEditorCmd picks them up
		m.compose.to.SetValue(ps.to)
		m.compose.cc.SetValue(ps.cc)
		m.compose.bcc.SetValue(ps.bcc)
		m.compose.subject.SetValue(ps.subject)
		if ps.cc != "" || ps.bcc != "" {
			m.compose.extraVisible = true
		}
		_ = prelude
		return m.launchEditorCmd()
	case "d":
		// Save to Drafts without sending.
		return m, m.saveDraftCmd(m.presendFrom(), ps.to, ps.cc, ps.subject, ps.body, m.attachments)
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

	f, err := os.CreateTemp("", "neomd-preview-*.html")
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
	f, err := os.CreateTemp("", "neomd-*.md")
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
	return m, tea.ExecProcess(cmd, func(execErr error) tea.Msg {
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

	chooserFile, err := os.CreateTemp("", "neomd-pick-*.txt")
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
	prelude := editor.ForwardPrelude(subject, e.From, e.Date.Format("Mon, 02 Jan 2006 15:04:05 -0700"), e.To, m.openBody)

	f, err := os.CreateTemp("", "neomd-*.md")
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
	return m, tea.ExecProcess(cmd, func(execErr error) tea.Msg {
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

	// Use Reply-To if present, else From
	to := e.ReplyTo
	if to == "" {
		to = e.From
	}

	subject := e.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
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

	prelude := editor.ReplyPrelude(to, cc, subject, e.From, m.openBody)

	f, err := os.CreateTemp("", "neomd-*.md")
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
	return m, tea.ExecProcess(cmd, func(execErr error) tea.Msg {
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

// extractEmailAddr returns the bare email address from "Name <addr>" or "addr".
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
// - Image files (.png, .jpg, …) are converted to ![](path) markdown refs so
//   goldmark renders them as <img> tags inline; the sender embeds them via CID.
// - Non-image files are returned as file attachment paths (appended at bottom).
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

	b.WriteString(styleHelp.Render("  enter send · p preview · ctrl+f from · a attach · D remove last · d draft · e edit · esc cancel"))
	return b.String()
}

func (m Model) viewInbox() string {
	var b strings.Builder

	// Account indicator (only shown when more than one account configured)
	activeTab := m.folders[m.activeFolderI]
	if m.offTabFolder != "" {
		activeTab = "" // deselect all tabs; off-tab folder shown separately
	}
	header := folderTabs(m.folders, activeTab, m.folderCounts)
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
		b.WriteString(fmt.Sprintf("  %s Loading…\n", m.spinner.View()))
	} else if len(m.emails) == 0 {
		b.WriteString(styleStatus.Render("  No messages.") + "\n")
	} else {
		b.WriteString(m.inbox.View())
	}

	b.WriteString("\n")
	if m.cmdMode {
		b.WriteString(viewCmdLine(m.cmdText, m.width))
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
	b.WriteString("\n" + readerHelp(isDraft))
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
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPrimary).
		Padding(1, 3).
		Width(60)

	title := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	key := lipgloss.NewStyle().Foreground(colorAuthorUnread).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colorDateCol)

	content := title.Render("Welcome to neomd!") + "\n\n" +
		"Your IMAP folders and screener lists have been\n" +
		"set up automatically.\n\n" +
		title.Render("Quick start") + "\n" +
		key.Render("  j/k") + "  navigate    " + key.Render("enter") + "  open email\n" +
		key.Render("  c") + "    compose      " + key.Render("r") + "      reply\n" +
		key.Render("  f") + "    forward      " + key.Render("R") + "      reply-all\n\n" +
		title.Render("Screener") + " " + dim.Render("(from inbox or reader)") + "\n" +
		key.Render("  I") + "  approve sender (stays in Inbox)\n" +
		key.Render("  O") + "  block sender   (moves to ScreenedOut)\n" +
		key.Render("  F") + "  mark as feed   (moves to Feed)\n" +
		key.Render("  P") + "  mark as paper  (moves to PaperTrail)\n\n" +
		dim.Render("Press ? anytime for all keybindings.") + "\n\n" +
		dim.Render("Press any key to continue.")

	rendered := box.Render(content)

	// Center vertically and horizontally
	lines := strings.Count(rendered, "\n") + 1
	padTop := (m.height - lines) / 2
	if padTop < 0 {
		padTop = 0
	}
	padLeft := (m.width - 60) / 2
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
