// Package ui contains the bubbletea TUI model for neomd.
package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/editor"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/screener"
	"github.com/sspaeti/neomd/internal/smtp"
)

// viewState is the current screen.
type viewState int

const (
	stateInbox   viewState = iota
	stateReading           // reading a single email
	stateCompose           // composing a new email
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
	sendDoneMsg   struct{ err error }
	screenDoneMsg struct{ err error }
	errMsg        struct{ err error }
	editorDoneMsg struct {
		to, subject, body string
		err               error
	}
)

// Model is the root bubbletea model.
type Model struct {
	cfg      *config.Config
	imapCli  *imap.Client
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

	// Compose
	compose composeModel

	// Status / error
	status  string
	isError bool
}

// New creates and initialises the TUI model.
func New(cfg *config.Config, imapCli *imap.Client, sc *screener.Screener) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return Model{
		cfg:      cfg,
		imapCli:  imapCli,
		screener: sc,
		state:    stateInbox,
		loading:  true,
		folders:  []string{"Inbox", "ToScreen", "Feed", "PaperTrail"},
		compose:  newComposeModel(),
		spinner:  sp,
	}
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
	default:
		return m.cfg.Folders.Inbox
	}
}

// ── Commands ─────────────────────────────────────────────────────────────

func (m Model) fetchFolderCmd(folder string) tea.Cmd {
	return func() tea.Msg {
		emails, err := m.imapCli.FetchHeaders(nil, folder, m.cfg.UI.InboxCount)
		if err != nil {
			return errMsg{err}
		}
		return emailsLoadedMsg{emails: emails, folder: folder}
	}
}

func (m Model) fetchBodyCmd(e *imap.Email) tea.Cmd {
	return func() tea.Msg {
		body, err := m.imapCli.FetchBody(nil, e.Folder, e.UID)
		if err != nil {
			return errMsg{err}
		}
		return bodyLoadedMsg{email: e, body: body}
	}
}

func (m Model) sendEmailCmd(to, subject, body string) tea.Cmd {
	h, p := splitAddr(m.cfg.Account.SMTP)
	cfg := smtp.Config{
		Host:     h,
		Port:     p,
		User:     m.cfg.Account.User,
		Password: m.cfg.Account.Password,
		From:     m.cfg.Account.From,
	}
	return func() tea.Msg {
		return sendDoneMsg{smtp.Send(cfg, to, subject, body)}
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
			if err := m.imapCli.MoveMessage(nil, folder, e.UID, dst); err != nil {
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
		cmd := setEmails(&m.inbox, msg.emails)
		return m, cmd

	case bodyLoadedMsg:
		m.loading = false
		m.openEmail = msg.email
		_ = loadEmailIntoReader(&m.reader, msg.email, msg.body, m.cfg.UI.Theme, m.width)
		m.state = stateReading
		// Mark as seen in background (best-effort)
		uid := msg.email.UID
		folder := msg.email.Folder
		go func() { _ = m.imapCli.MarkSeen(nil, folder, uid) }()
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
		if strings.TrimSpace(msg.body) == "" {
			m.status = "Cancelled (empty body)."
			m.state = stateInbox
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.sendEmailCmd(msg.to, msg.subject, msg.body))

	case tea.KeyMsg:
		switch m.state {
		case stateInbox:
			return m.updateInbox(msg)
		case stateReading:
			return m.updateReader(msg)
		case stateCompose:
			return m.updateCompose(msg)
		}
	}

	return m, nil
}

func (m Model) updateInbox(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.status = ""
	m.isError = false

	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit

	case "tab":
		m.activeFolderI = (m.activeFolderI + 1) % len(m.folders)
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))

	case "c":
		m.state = stateCompose
		m.compose.reset()
		return m, nil

	case "r":
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.activeFolder()))

	case "enter":
		e := selectedEmail(m.inbox)
		if e == nil {
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.fetchBodyCmd(e))

	case "I", "O", "F", "P":
		if m.folders[m.activeFolderI] != "ToScreen" {
			break
		}
		e := selectedEmail(m.inbox)
		if e == nil {
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.screenerCmd(e, msg.String()))
	}

	// Forward remaining keys (j/k navigation, filter /) to list
	var cmd tea.Cmd
	m.inbox, cmd = m.inbox.Update(msg)
	return m, cmd
}

func (m Model) updateReader(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.state = stateInbox
		return m, nil
	}
	var cmd tea.Cmd
	m.reader, cmd = m.reader.Update(msg)
	return m, cmd
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
	prelude := editor.Prelude(to, subject)

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
	}
	return ""
}

func (m Model) viewInbox() string {
	var b strings.Builder
	b.WriteString(folderTabs(m.folders, m.folders[m.activeFolderI]) + "\n")
	b.WriteString(styleSeparator.Render(strings.Repeat("─", m.width)) + "\n")

	if m.loading {
		b.WriteString(fmt.Sprintf("  %s Loading…\n", m.spinner.View()))
	} else if len(m.emails) == 0 {
		b.WriteString(styleStatus.Render("  No messages.") + "\n")
	} else {
		b.WriteString(m.inbox.View())
	}

	b.WriteString("\n")
	if m.status != "" {
		b.WriteString(statusBar(m.status, m.isError))
	} else {
		b.WriteString(inboxHelp(m.folders[m.activeFolderI]))
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
