package ui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/editor"
)

// neomdCmd is a registered colon-command (like vim's :command).
type neomdCmd struct {
	name    string   // full name, e.g. "screen-all"
	aliases []string // short forms accepted, e.g. ["sa", "screen-a"]
	desc    string
	// run is called when the command is executed; m is the current model.
	run func(m *Model) (tea.Model, tea.Cmd)
	// runArgs, when set, is used instead of run and receives everything
	// typed after the command word (trimmed), e.g. ":merge My Title".
	runArgs func(m *Model, args string) (tea.Model, tea.Cmd)
}

// cmdRegistry is the list of all available colon-commands.
// Add new commands here — they become automatically available in the
// command line with tab-completion.
var cmdRegistry []neomdCmd

func init() {
	cmdRegistry = []neomdCmd{
		{
			name:    "screen",
			aliases: []string{"s"},
			desc:    "screen currently loaded emails only (up to inbox_count)",
			run: func(m *Model) (tea.Model, tea.Cmd) {
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
				m.pendingMoves = moves
				m.status = screenSummary(moves) + "  · y to apply, n to cancel"
				return m, nil
			},
		},
		{
			name:    "screen-all",
			aliases: []string{"sa", "screen-a"},
			desc:    "fetch and screen EVERY inbox email, no limit (use after updating screener lists)",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				if err := m.validateScreenerSafety(); err != nil {
					m.status = err.Error()
					m.isError = true
					return m, nil
				}
				m.loading = true
				return m, m.deepScreenCmd()
			},
		},
		{
			name:    "scan-spy-pixels",
			aliases: []string{"ssp"},
			desc:    "scan current folder for tracking pixels (background, skips already scanned)",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				m.status = "Scanning for spy pixels…"
				return m, m.spyScanCmd()
			},
		},
		{
			name:    "reload",
			aliases: []string{"r", "re"},
			desc:    "reload / refresh the current folder",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				m.loading = true
				return m, m.fetchFolderCmd(m.activeFolder())
			},
		},
		{
			name:    "mark-read",
			aliases: []string{"mr"},
			desc:    "mark all emails in current folder as read",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				cmd := m.markAllSeenCmd()
				if cmd == nil {
					m.status = "All already read."
					return m, nil
				}
				m.loading = true
				return m, tea.Batch(m.spinner.Tick, cmd)
			},
		},
		{
			name:    "check",
			aliases: []string{"ch"},
			desc:    "show screener classification for the selected email (diagnostic)",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				e := selectedEmail(m.inbox)
				if e == nil {
					m.status = "No email selected."
					return m, nil
				}
				cat, addr := m.screener.ClassifyDebug(e.From)
				m.status = fmt.Sprintf("from=%q  addr=%q  → %s", e.From, addr, cat)
				return m, nil
			},
		},
		{
			name:    "reset-toscreen",
			aliases: []string{"rts"},
			desc:    "move all ToScreen emails back to Inbox (then run screen-all to reclassify)",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				m.loading = true
				return m, tea.Batch(m.spinner.Tick, m.resetToScreenSearchCmd())
			},
		},
		{
			name:    "delete-all",
			aliases: []string{"da"},
			desc:    "permanently delete ALL emails in the current folder (y/n confirmation)",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				m.loading = true
				return m, tea.Batch(m.spinner.Tick, m.deleteAllSearchCmd())
			},
		},
		{
			name:    "empty-trash",
			aliases: []string{"et"},
			desc:    "permanently delete ALL emails in Trash (y/n confirmation)",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				m.loading = true
				return m, tea.Batch(m.spinner.Tick, m.emptyTrashSearchCmd())
			},
		},
		{
			name:    "create-folders",
			aliases: []string{"cf"},
			desc:    "create any missing IMAP folders defined in config (safe to run multiple times)",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				m.loading = true
				return m, tea.Batch(m.spinner.Tick, m.ensureFoldersCmd())
			},
		},
		{
			name:    "everything",
			aliases: []string{"ev"},
			desc:    "show latest 50 emails across all folders (newest first)",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				m.loading = true
				return m, tea.Batch(m.spinner.Tick, m.fetchEverythingCmd())
			},
		},
		{
			name:    "search",
			aliases: []string{"se"},
			desc:    "IMAP search all emails across all configured folders (From + Subject + To)",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				m.imapSearchActive = true
				m.imapSearchText = ""
				m.imapSearchResults = false
				return m, nil
			},
		},
		{
			name:    "go-spam",
			aliases: []string{"spam"},
			desc:    "open Spam folder (not in tab rotation — use :go-spam to visit)",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				m.loading = true
				m.status = "Spam folder — press R to reload, tab to leave"
				return m, tea.Batch(m.spinner.Tick, m.fetchFolderCmd(m.cfg.Folders.Spam))
			},
		},
		{
			name:    "debug",
			aliases: []string{"dbg"},
			desc:    "write diagnostic report to /tmp/neomd/debug.log and open it",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				return m, m.writeDebugReport()
			},
		},
		{
			name:    "recover",
			aliases: []string{"rec"},
			desc:    "reopen the most recent compose backup from ~/.cache/neomd/drafts/",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				dir := config.DraftsBackupDir()
				files := listBackupsByAge(dir)
				if len(files) == 0 {
					m.status = "No draft backups found in " + dir
					return m, nil
				}

				// Read the most recent backup (list is oldest-first).
				raw, err := os.ReadFile(files[len(files)-1].path)
				if err != nil {
					m.status = "read backup: " + err.Error()
					m.isError = true
					return m, nil
				}
				to, cc, bcc, from, subject, body := editor.ParseHeaders(string(raw))

				// Pre-fill compose fields.
				m.compose.reset()
				m.presendFromI = m.defaultFromIndex()
				if idx := m.matchFromAddress(from); idx >= 0 {
					m.presendFromI = idx
				}
				m.compose.to.SetValue(to)
				m.compose.cc.SetValue(cc)
				m.compose.bcc.SetValue(bcc)
				m.compose.subject.SetValue(subject)
				if cc != "" || bcc != "" {
					m.compose.extraVisible = true
				}
				m.compose.step = 3

				return m.launchEditorWithBodyCmd(to, cc, bcc, subject, body)
			},
		},
		{
			name:    "thread",
			aliases: []string{"t"},
			desc:    "show full conversation for the selected email (across folders)",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				e := selectedEmail(m.inbox)
				if e == nil {
					m.status = "No email selected."
					return m, nil
				}
				m.loading = true
				return m, tea.Batch(m.spinner.Tick, m.fetchConversationCmd(e))
			},
		},
		{
			name:    "notify-test",
			aliases: []string{"nt"},
			desc:    "fire a single test desktop notification using the current [notifications] config (diagnostic)",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				if !m.notifier.Enabled() {
					m.status = "Notifications disabled. Set [notifications].enabled = true in config.toml."
					m.isError = true
					return m, nil
				}
				if err := m.notifier.Send("neomd: test", "If you can see this, notify-send works."); err != nil {
					m.status = "notify-send failed: " + err.Error()
					m.isError = true
					return m, nil
				}
				m.status = "Test notification sent — check your desktop notifications. Listening folders: " +
					strings.Join(m.cfg.Notifications.Resolved().Folders, ", ")
				return m, nil
			},
		},
		{
			name:    "merge",
			aliases: []string{"mg"},
			desc:    "merge marked/cursor emails into a titled group: :merge <title> (HEY-style merge threads)",
			runArgs: func(m *Model, args string) (tea.Model, tea.Cmd) {
				return m.mergeCmd(args, false)
			},
		},
		{
			name:    "merge-sender",
			aliases: []string{"mgs"},
			desc:    "like :merge, plus future mail from the cursor email's sender joins automatically",
			runArgs: func(m *Model, args string) (tea.Model, tea.Cmd) {
				return m.mergeCmd(args, true)
			},
		},
		{
			name:    "unmerge",
			aliases: []string{"umg"},
			desc:    "on a ≡ row: dissolve the merge (y/n); inside an opened merge: remove the cursor email",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				return m.unmergeCmd()
			},
		},
		{
			name:    "quit",
			aliases: []string{"q"},
			desc:    "quit neomd",
			run: func(m *Model) (tea.Model, tea.Cmd) {
				return m, tea.Quit
			},
		},
	}
}

// screenSummary builds the "N→Folder …" part of the S/screen-all preview.
func screenSummary(moves []autoScreenMove) string {
	counts := map[string]int{}
	for _, mv := range moves {
		counts[mv.dst]++
	}
	s := ""
	for dst, n := range counts {
		s += " " + formatInt(n) + "→" + dst
	}
	return "Would move " + formatInt(len(moves)) + " email(s):" + s
}

func formatInt(n int) string { return fmt.Sprintf("%d", n) }

// matchCmds returns all commands whose name or any alias has text as a prefix.
// When text is empty, all commands are returned (for tab-cycling).
func matchCmds(text string) []*neomdCmd {
	word, _ := splitCmdInput(text)
	lower := strings.ToLower(word)
	var out []*neomdCmd
	for i := range cmdRegistry {
		c := &cmdRegistry[i]
		if lower == "" || strings.HasPrefix(c.name, lower) {
			out = append(out, c)
			continue
		}
		for _, a := range c.aliases {
			if strings.HasPrefix(a, lower) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// matchCmd returns the first matching command (for enter / ghost completion).
func matchCmd(text string) *neomdCmd {
	if text == "" {
		return nil
	}
	if m := matchCmds(text); len(m) > 0 {
		return m[0]
	}
	return nil
}

// viewCmdLine renders the command-line bar shown at the bottom of the inbox.
// When input is empty or has multiple matches it shows a tab-cycle menu above.
func viewCmdLine(text string, width int) string {
	matches := matchCmds(text)
	first := matchCmd(text) // nil when empty

	prefix := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true).Render(":")
	inputS := lipgloss.NewStyle().Foreground(colorText).Render(text)

	// Ghost completion: rest of first matched name
	ghost := ""
	if first != nil && text != "" {
		lower := strings.ToLower(firstWordOf(text))
		if strings.HasPrefix(first.name, lower) && len(first.name) > len(lower) {
			ghost = lipgloss.NewStyle().Foreground(colorMuted).Render(first.name[len(lower):])
		}
	}

	cursor := lipgloss.NewStyle().Foreground(colorPrimary).Render("█")

	// Inline description / error
	desc := ""
	if first != nil {
		desc = lipgloss.NewStyle().Foreground(colorMuted).Render("   — " + first.desc)
	} else if text != "" {
		desc = lipgloss.NewStyle().Foreground(colorError).Render("   unknown command")
	}

	cmdLine := "  " + prefix + inputS + ghost + cursor + desc

	// When empty or multiple matches: show a compact menu above the command line
	// so the user can see what's available and tab-cycle through them.
	if (len(matches) > 1 && !strings.Contains(strings.TrimSpace(text), " ")) || text == "" {
		nameStyle := lipgloss.NewStyle().Foreground(colorPrimary)
		dimStyle := lipgloss.NewStyle().Foreground(colorMuted)
		var parts []string
		for _, c := range matches {
			entry := nameStyle.Render(c.name)
			if len(c.aliases) > 0 {
				entry += dimStyle.Render(" (" + strings.Join(c.aliases, ",") + ")")
			}
			parts = append(parts, entry)
		}
		hint := dimStyle.Render("tab to cycle · enter to run · esc cancel")
		menu := "  " + strings.Join(parts, dimStyle.Render("  ·  ")) + "    " + hint
		return menu + "\n" + cmdLine
	}

	_ = width
	return cmdLine
}

// splitCmdInput separates ":merge My Title" into ("merge", "My Title").
func splitCmdInput(input string) (word, args string) {
	input = strings.TrimSpace(input)
	word, args, _ = strings.Cut(input, " ")
	return word, strings.TrimSpace(args)
}

func firstWordOf(text string) string { w, _ := splitCmdInput(text); return w }

// mergeCmd implements :merge / :merge-sender.
func (m *Model) mergeCmd(title string, withSender bool) (tea.Model, tea.Cmd) {
	title = strings.TrimSpace(title)
	if title == "" {
		m.status = "usage: :merge <title>   (mark emails with m first, or use the cursor email)"
		m.isError = true
		return m, nil
	}
	targets := m.targetEmails()
	if len(targets) == 0 {
		m.status = "No email selected."
		m.isError = true
		return m, nil
	}
	if withSender {
		e := selectedEmail(m.inbox)
		addr := ""
		if e != nil {
			addr = normalizedSender(e.From)
		}
		if addr == "" {
			m.status = "Cursor email has no usable From address."
			m.isError = true
			return m, nil
		}
		m.merges.SetSender(title, addr)
	}
	var ids []string
	skipped := 0
	for _, e := range targets {
		if e.MessageID == "" {
			skipped++
			continue
		}
		ids = append(ids, e.MessageID)
	}
	added := m.merges.Add(title, ids...)
	if withSender {
		added += m.applySenderRules(m.emails)
	}
	if err := m.merges.Save(); err != nil {
		m.status = "merges.toml: " + err.Error()
		m.isError = true
		return m, nil
	}
	m.markedUIDs = make(map[uint32]bool)
	m.isError = false
	m.status = fmt.Sprintf("Merged %d email(s) into %q", added, title)
	if skipped > 0 {
		m.status += fmt.Sprintf(" · %d skipped (no Message-ID)", skipped)
	}
	if withSender {
		m.status += " · sender rule saved"
	}
	return m, m.applyFilter()
}

// unmergeCmd implements :unmerge.
func (m *Model) unmergeCmd() (tea.Model, tea.Cmd) {
	if m.inMergeView() {
		e := selectedEmail(m.inbox)
		if e == nil {
			m.status = "No email selected."
			m.isError = true
			return m, nil
		}
		if !m.merges.Remove(e.MessageID) {
			m.status = "Not an explicit member (absorbed reply) — nothing to remove."
			m.isError = true
			return m, nil
		}
		if err := m.merges.Save(); err != nil {
			m.status = "merges.toml: " + err.Error()
			m.isError = true
			return m, nil
		}
		kept := m.emails[:0:0]
		for _, x := range m.emails {
			if x.UID != e.UID || x.Folder != e.Folder {
				kept = append(kept, x)
			}
		}
		m.emails = kept
		m.isError = false
		m.status = "Removed from merge."
		return m, m.applyFilter()
	}
	it, ok := selectedItem(m.inbox)
	if !ok || it.merge == nil {
		m.status = ":unmerge works on a ≡ merged row or inside an opened merge."
		m.isError = true
		return m, nil
	}
	m.pendingUnmerge = it.merge.title
	m.isError = false
	m.status = fmt.Sprintf("Dissolve merge %q (%d in this folder)? y/n", it.merge.title, len(it.merge.members))
	return m, nil
}

// titleCompletions returns ":merge <title>" candidates for the typed text,
// or nil when the command is not one that takes a merge title.
func (m Model) titleCompletions(text string) []string {
	word, args := splitCmdInput(text)
	if !strings.Contains(text, " ") {
		return nil
	}
	c := matchCmd(word)
	if c == nil || (c.name != "merge" && c.name != "merge-sender") {
		return nil
	}
	var out []string
	for _, t := range m.merges.Titles() {
		if strings.HasPrefix(strings.ToLower(t), strings.ToLower(args)) {
			out = append(out, c.name+" "+t)
		}
	}
	return out
}
