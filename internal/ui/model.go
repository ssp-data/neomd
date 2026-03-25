// Package ui contains the bubbletea TUI model for neomd.
package ui

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

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
	stateHelp              // help overlay
)

// async message types
type (
	emailsLoadedMsg struct {
		emails []imap.Email
		folder string
	}
	bodyLoadedMsg struct {
		email *imap.Email
		body  string
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
	moveDoneMsg       struct{ err error }
	batchDoneMsg      struct{ err error }
	toggleSeenDoneMsg struct{ uid uint32; seen bool; err error }
	errMsg            struct{ err error }
	editorDoneMsg     struct {
		to, subject, body string
		err               error
		aborted           bool // true when file was unchanged (ZQ / :q!)
	}
)

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

	// Inbox
	inbox   list.Model
	emails  []imap.Email
	spinner spinner.Model

	// Reader
	reader    viewport.Model
	openEmail *imap.Email
	openBody  string // plain/markdown body of the open email (for external viewer)

	// Compose
	compose composeModel

	// Status / error
	status  string
	isError bool

	// Auto-screen dry-run: populated by S, cleared by y/n
	pendingMoves []autoScreenMove

	// Marked emails for batch operations (UID → true)
	markedUIDs map[uint32]bool

	// Chord prefix: "g" or "M" while waiting for second key
	pendingKey string

	// prevState is the state to return to when closing the help overlay
	prevState viewState

	// helpSearch is the live filter string typed in the help overlay
	helpSearch string

	// cmdMode / cmdText / cmdTabI implement vim-style ":" command line.
	cmdMode bool
	cmdText string
	cmdTabI int // cycle index for tab-completion

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
		folders:     []string{"Inbox", "ToScreen", "Feed", "PaperTrail", "Archive", "Waiting", "Someday", "Scheduled", "Sent", "Trash", "ScreenedOut"},
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

// imapCli returns the IMAP client for the active account.
func (m Model) imapCli() *imap.Client {
	if m.accountI < len(m.clients) {
		return m.clients[m.accountI]
	}
	return m.clients[0]
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.fetchFolderCmd(m.activeFolder()),
	)
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
		body, err := m.imapCli().FetchBody(nil, e.Folder, e.UID)
		if err != nil {
			return errMsg{err}
		}
		return bodyLoadedMsg{email: e, body: body}
	}
}

func (m Model) sendEmailCmd(to, subject, body string) tea.Cmd {
	h, p := splitAddr(m.activeAccount().SMTP)
	cfg := smtp.Config{
		Host:     h,
		Port:     p,
		User:     m.activeAccount().User,
		Password: m.activeAccount().Password,
		From:     m.activeAccount().From,
	}
	return func() tea.Msg {
		return sendDoneMsg{smtp.Send(cfg, to, subject, body)}
	}
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
		return moveDoneMsg{m.imapCli().MoveMessage(nil, src, uid, dst)}
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
		for i, mv := range moves {
			if err := m.imapCli().MoveMessage(nil, mv.folder, mv.uid, dst); err != nil {
				return batchDoneMsg{fmt.Errorf("stopped after %d/%d: %w", i, len(moves), err)}
			}
		}
		return batchDoneMsg{}
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
			}
			if err != nil {
				return batchDoneMsg{fmt.Errorf("stopped after %d/%d: %w", i, len(ops), err)}
			}
			if o.dst != "" && o.dst != o.srcFolder {
				if err := m.imapCli().MoveMessage(nil, o.srcFolder, o.uid, o.dst); err != nil {
					return batchDoneMsg{fmt.Errorf("stopped after %d/%d: %w", i, len(ops), err)}
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
				return batchDoneMsg{err}
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
				return batchDoneMsg{err}
			}
		}
		return batchDoneMsg{}
	}
}

// previewAutoScreen classifies the current inbox emails in-memory (no IMAP)
// and returns the planned moves without executing anything.
func (m Model) previewAutoScreen() []autoScreenMove {
	inboxFolder := m.cfg.Folders.Inbox
	var moves []autoScreenMove
	for i := range m.emails {
		e := &m.emails[i]
		cat := m.screener.Classify(e.From)
		var dst string
		switch cat {
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
			if err := m.imapCli().MoveMessage(nil, src, uid, dst); err != nil {
				return batchDoneMsg{fmt.Errorf("stopped after %d/%d: %w", i, len(uids), err)}
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
		f.Archive, f.Waiting, f.Scheduled, f.Someday,
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

// deleteAllExecCmd permanently deletes all given UIDs from folder.
func (m Model) deleteAllExecCmd(folder string, uids []uint32) tea.Cmd {
	return func() tea.Msg {
		return batchDoneMsg{m.imapCli().ExpungeAll(nil, folder, uids)}
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

// execAutoScreenCmd performs the IMAP moves for a pre-approved list of moves.
func (m Model) execAutoScreenCmd(moves []autoScreenMove) tea.Cmd {
	src := m.cfg.Folders.Inbox
	return func() tea.Msg {
		for i, mv := range moves {
			if err := m.imapCli().MoveMessage(nil, src, mv.email.UID, mv.dst); err != nil {
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
		}
		if addErr != nil {
			return errMsg{addErr}
		}
		if dst != "" && dst != folder {
			if err := m.imapCli().MoveMessage(nil, folder, e.UID, dst); err != nil {
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
		cmd := m.sortEmails() // applies sort and sets list items
		return m, tea.Batch(cmd, m.fetchFolderCountsCmd())

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
		if len(msg.created) == 0 {
			m.status = "All folders already exist."
		} else {
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
		_ = loadEmailIntoReader(&m.reader, msg.email, msg.body, m.cfg.UI.Theme, m.width)
		m.state = stateReading
		// Mark as seen in background (best-effort)
		uid := msg.email.UID
		folder := msg.email.Folder
		go func() { _ = m.imapCli().MarkSeen(nil, folder, uid) }()
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
		m.status = "Moved."
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
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.sendEmailCmd(msg.to, msg.subject, msg.body))

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
		case stateHelp:
			return m.updateHelp(msg)
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
		case "enter":
			m.cmdMode = false
			input := strings.TrimSpace(m.cmdText)
			m.cmdText = ""
			if cmd := matchCmd(input); cmd != nil {
				result, c := cmd.run(&m)
				return result, c
			}
			if input != "" {
				m.status = "Unknown command: " + input
				m.isError = true
			}
		case "backspace", "ctrl+h":
			runes := []rune(m.cmdText)
			if len(runes) > 0 {
				m.cmdText = string(runes[:len(runes)-1])
			}
			m.cmdTabI = 0
		case "tab":
			matches := matchCmds(m.cmdText)
			if len(matches) > 0 {
				m.cmdText = matches[m.cmdTabI%len(matches)].name
				m.cmdTabI++
			}
		default:
			if len(key) == 1 {
				m.cmdText += key
				m.cmdTabI = 0 // reset cycle on new input
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
		m.status = "go to:  gi inbox  ga archive  gf feed  gp papertrail  gt trash  gs sent  gk toscreen  go screened-out  gw waiting  gm someday  gg top"
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

	case "U": // clear all marks
		m.markedUIDs = make(map[uint32]bool)
		return m, setEmails(&m.inbox, m.emails, m.markedUIDs)

	// ── Screener actions — operate on marked emails or cursor email ──
	case "I", "O", "F", "P":
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

	// ── Navigation ──────────────────────────────────────────────────
	case "tab", "L":
		m.activeFolderI = (m.activeFolderI + 1) % len(m.folders)
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))

	case "shift+tab", "H":
		m.activeFolderI = (m.activeFolderI - 1 + len(m.folders)) % len(m.folders)
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

	case " ": // mark/unmark current email for batch, advance cursor
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

	case "N": // toggle read/unread on marked emails (or cursor email)
		targets := m.targetEmails()
		if len(targets) == 0 {
			break
		}
		if len(targets) == 1 && len(m.markedUIDs) == 0 {
			// single optimistic update — no reload needed
			next := m.inbox.Index() + 1
			if next < len(m.inbox.Items()) {
				m.inbox.Select(next)
			}
			return m, m.toggleSeenCmd(&targets[0])
		}
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.batchToggleSeenCmd(targets))
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

// handleChord dispatches two-key sequences (g<x> and M<x>).
func (m Model) handleChord(prefix, key string) (tea.Model, tea.Cmd) {
	switch prefix {
	case "g":
		if key == "g" { // gg = top of list
			m.inbox.Select(0)
			return m, nil
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
					if i == m.activeFolderI {
						return m, nil
					}
					m.activeFolderI = i
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
	case "O":
		return m.openInExternalViewer()
	case "r":
		if m.openEmail != nil {
			return m.launchReplyCmd()
		}
	}
	var cmd tea.Cmd
	m.reader, cmd = m.reader.Update(msg)
	return m, cmd
}

// openInExternalViewer renders the open email as HTML, writes it to a temp
// file, and opens it with $BROWSER (falling back to w3m), same pattern as
// newsboat's "open in browser" binding.
func (m Model) openInExternalViewer() (tea.Model, tea.Cmd) {
	body := m.openBody
	if body == "" {
		return m, nil
	}

	browser := os.Getenv("BROWSER")
	if browser == "" {
		browser = "w3m"
	}

	// Render markdown → HTML so links are clickable in w3m.
	htmlBody, err := render.ToHTML(body)
	if err != nil {
		htmlBody = "<pre>" + body + "</pre>"
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

	cmd := exec.Command(browser, tmpPath)
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		os.Remove(tmpPath)
		if err != nil {
			return errMsg{err}
		}
		return nil
	})
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

func (m Model) updateCompose(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.state = stateInbox
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

func (m Model) launchEditorCmd() (tea.Model, tea.Cmd) {
	to := m.compose.to.Value()
	subject := m.compose.subject.Value()
	prelude := editor.Prelude(to, subject, m.cfg.UI.Signature)

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
		return editorDoneMsg{to: to, subject: subject, body: string(raw)}
	})
}

func (m Model) launchReplyCmd() (tea.Model, tea.Cmd) {
	e := m.openEmail
	to := e.From
	subject := e.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	prelude := editor.ReplyPrelude(to, subject, e.From, m.openBody)

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
		return editorDoneMsg{to: to, subject: subject, body: string(raw)}
	})
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
	case stateHelp:
		return m.viewHelp()
	}
	return ""
}

func (m Model) viewInbox() string {
	var b strings.Builder

	// Account indicator (only shown when more than one account configured)
	header := folderTabs(m.folders, m.folders[m.activeFolderI], m.folderCounts)
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
	b.WriteString("\n" + readerHelp())
	return b.String()
}

func (m Model) viewCompose() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("  New Message") + "\n")
	b.WriteString(styleSeparator.Render(strings.Repeat("─", m.width)) + "\n\n")
	b.WriteString(m.compose.view() + "\n\n")
	b.WriteString(composeHelp(int(m.compose.step)))
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
