package ui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sspaeti/neomd/internal/imap"
)

// keywordTestKeyword is the flag the probe writes and removes again. Fixed so
// a crashed run's leftover is removed by the next run.
const keywordTestKeyword = "NeomdTest"

// keywordTestTarget captures everything the probe needs about the test
// message by value — the tea.Cmd closure must not hold a pointer into
// m.emails (the UI re-sorts it in place).
type keywordTestTarget struct {
	account string
	host    string
	folder  string
	uid     uint32
	subject string
}

// keywordTestDoneMsg reports the IMAP keyword support probe result (:kt).
type keywordTestDoneMsg struct {
	result *imap.KeywordProbeResult
	target keywordTestTarget
}

// keywordTestModel is the probe dialog's state on Model (the composeModel
// pattern: the state lives here, next to its methods; Model keeps one
// field, keywordTest).
type keywordTestModel struct {
	running bool
	result  *imap.KeywordProbeResult
	target  keywordTestTarget
}

// openKeywordTest opens the IMAP keyword support probe dialog (space k or
// :keyword-test) and starts the round trip on the selected email (first email
// as fallback). keep=true leaves the keyword on the message afterwards.
func (m Model) openKeywordTest(keep bool) (tea.Model, tea.Cmd) {
	cli := m.imapCli()
	if cli == nil {
		m.status = "Keyword test: no IMAP connection for this account."
		m.isError = true
		return m, nil
	}
	e := selectedEmail(m.inbox)
	if e == nil {
		if items := m.inbox.Items(); len(items) > 0 {
			if item, ok := items[0].(emailItem); ok {
				e = &item.email
			}
		}
	}
	if e == nil {
		m.status = "Keyword test: no email to attach the probe to."
		m.isError = true
		return m, nil
	}
	m.prevState = m.state
	m.state = stateKeywordTest
	m.keywordTest.running = true
	m.keywordTest.result = nil
	m.keywordTest.target = keywordTestTarget{
		account: m.activeAccountName(),
		host:    cli.Addr(),
		folder:  e.Folder,
		uid:     e.UID,
		subject: e.Subject,
	}
	return m, tea.Batch(m.spinner.Tick, keywordTestCmd(cli, m.keywordTest.target, keep))
}

// keywordTestCmd runs the IMAP round trip off the UI thread and reports back
// via keywordTestDoneMsg. keep=true leaves the keyword on the message so
// persistence can be verified across sessions.
func keywordTestCmd(cli *imap.Client, target keywordTestTarget, keep bool) tea.Cmd {
	return func() tea.Msg {
		return keywordTestDoneMsg{
			result: cli.KeywordProbe(context.Background(), target.folder, target.uid, keywordTestKeyword, keep),
			target: target,
		}
	}
}

func (m Model) updateKeywordTest(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "h":
		m.state = m.prevState
		if m.state == stateKeywordTest {
			m.state = stateInbox
		}
		m.keywordTest.running = false
		m.keywordTest.result = nil
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "r", "K":
		if m.keywordTest.running {
			return m, nil
		}
		cli := m.imapCli()
		if cli == nil {
			m.status = "Keyword test: no IMAP connection for this account."
			m.isError = true
			return m, nil
		}
		// K re-runs in keep mode: the keyword stays on the message so its
		// persistence can be checked later (r re-run removes it again).
		keep := msg.String() == "K"
		m.keywordTest.running = true
		m.keywordTest.result = nil
		return m, tea.Batch(m.spinner.Tick, keywordTestCmd(cli, m.keywordTest.target, keep))
	}
	return m, nil
}

// handleKeywordTestDone stores the probe result for the dialog; dropped
// when the dialog was closed before the probe finished.
func (m Model) handleKeywordTestDone(msg keywordTestDoneMsg) (tea.Model, tea.Cmd) {
	if m.state != stateKeywordTest {
		return m, nil // dialog closed before the probe finished
	}
	m.keywordTest.running = false
	m.keywordTest.result = msg.result
	return m, nil
}

// keywordTestVerdict summarizes a probe result into one line for the dialog.
func keywordTestVerdict(r *imap.KeywordProbeResult) (string, bool) {
	switch {
	case r == nil:
		return "", false
	case r.Err != "" && len(r.Steps) == 0:
		return "Probe failed before any step ran: " + r.Err, false
	case r.Kept && r.StoreSupported && r.SearchSupported:
		return "Keyword support confirmed — NeomdTest left on the message (keep mode). Re-run with r to remove it.", true
	case r.StoreSupported && r.SearchSupported && r.CleanupOK && r.WildcardAllowed:
		return "Full keyword support — STORE, SEARCH and \\* advertised. Server-side tagging will work.", true
	case r.StoreSupported && r.SearchSupported && r.CleanupOK:
		return "Keywords work (STORE + SEARCH OK), but \\* is not advertised — this server still accepts new keywords.", true
	case r.StoreSupported && !r.CleanupOK:
		return "STORE works but cleanup failed — the test keyword may remain on the message.", false
	case r.StoreSupported && !r.SearchSupported:
		return "Keywords are stored but UID SEARCH KEYWORD does not find them — search-based tag views would not work.", false
	case !r.StoreSupported:
		return "Arbitrary keywords are rejected — this server cannot be used for IMAP-keyword tagging.", false
	default:
		return "Partial support — see the step details above.", false
	}
}

var (
	keywordTitleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7E9CD8"))
	keywordDimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#727169"))
	keywordOKStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#98BB6C"))
	keywordErrStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#E46876"))
	keywordBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#54546D")).
				Padding(1, 2)
)

// viewKeywordTest renders the centered probe dialog.
func (m Model) viewKeywordTest() string {
	t := m.keywordTest.target
	w := 74

	var lines []string
	lines = append(lines, keywordTitleStyle.Render("IMAP Keyword (Tag) Support Test"), "")
	lines = append(lines, fmt.Sprintf("%s %s · UID %d", keywordDimStyle.Render("Account  "), fmt.Sprintf("%s (%s)", t.account, t.host), t.uid))
	lines = append(lines, fmt.Sprintf("%s %s", keywordDimStyle.Render("Mailbox  "), t.folder))
	lines = append(lines, fmt.Sprintf("%s %s", keywordDimStyle.Render("Message  "), truncate(t.subject, 58)))
	lines = append(lines, fmt.Sprintf("%s %s", keywordDimStyle.Render("Keyword  "), keywordTestKeyword))

	if m.keywordTest.running || m.keywordTest.result == nil {
		lines = append(lines, "",
			fmt.Sprintf(" %s Probing %s …", m.spinner.View(), t.host))
	} else {
		r := m.keywordTest.result
		lines = append(lines, "", keywordDimStyle.Render("── Server capability ──"))
		lines = append(lines, fmt.Sprintf(" PERMANENTFLAGS  %s", truncate(strings.Join(r.PermanentFlags, " "), 54)))
		if r.WildcardAllowed {
			lines = append(lines, " "+keywordOKStyle.Render("✓ new keywords allowed (\\* wildcard advertised)"))
		} else {
			lines = append(lines, " "+keywordErrStyle.Render("✗ \\* wildcard NOT advertised — server may reject unknown keywords"))
		}
		if len(r.MailboxKeywords) > 0 {
			lines = append(lines, fmt.Sprintf(" Keywords in use: %s", truncate(strings.Join(r.MailboxKeywords, ", "), 52)))
		}

		lines = append(lines, "", keywordDimStyle.Render("── Probe steps ──"))
		for _, s := range r.Steps {
			mark := keywordErrStyle.Render("✗")
			name := s.Name
			if s.OK {
				mark = keywordOKStyle.Render("✓")
			} else {
				name = keywordErrStyle.Render(name)
			}
			line := fmt.Sprintf(" %s %s", mark, name)
			detail := s.Detail
			if s.Err != "" {
				detail = s.Err
			}
			if detail != "" {
				line += keywordDimStyle.Render("  — " + truncate(detail, 52))
			}
			lines = append(lines, line)
		}

		verdict, good := keywordTestVerdict(r)
		lines = append(lines, "")
		vs := keywordErrStyle.Render
		if good {
			vs = keywordOKStyle.Render
		}
		lines = append(lines, " "+vs("Verdict: "+truncate(verdict, 64)))

		if r.Err != "" {
			lines = append(lines, " "+keywordErrStyle.Render(truncate("Fatal: "+r.Err, 66)))
		}
	}

	lines = append(lines, "", keywordDimStyle.Render("esc/q close · r re-run (removes a kept keyword) · K re-run keeping the tag"))

	box := keywordBorderStyle.Width(w).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
