package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/editor"
)

// neomdCmd is a registered colon-command (like vim's :command).
type neomdCmd struct {
	name    string // full name, e.g. "screen-all"
	aliases []string // short forms accepted, e.g. ["sa", "screen-a"]
	desc    string
	// run is called when the command is executed; m is the current model.
	run func(m *Model) (tea.Model, tea.Cmd)
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
				m.loading = true
				return m, m.deepScreenCmd()
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
			desc:    "IMAP search all emails in current folder (From + Subject)",
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
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) == 0 {
					m.status = "No draft backups found in " + dir
					return m, nil
				}
				// Sort by mod time descending (newest first).
				type fileEntry struct {
					path    string
					modTime int64
				}
				var files []fileEntry
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					info, err := e.Info()
					if err != nil {
						continue
					}
					files = append(files, fileEntry{
						path:    filepath.Join(dir, e.Name()),
						modTime: info.ModTime().Unix(),
					})
				}
				if len(files) == 0 {
					m.status = "No draft backups found in " + dir
					return m, nil
				}
				sort.Slice(files, func(i, j int) bool { return files[i].modTime > files[j].modTime })

				// Read the most recent backup.
				raw, err := os.ReadFile(files[0].path)
				if err != nil {
					m.status = "read backup: " + err.Error()
					m.isError = true
					return m, nil
				}
				to, cc, bcc, subject, body := editor.ParseHeaders(string(raw))

				// Pre-fill compose fields.
				m.compose.reset()
				m.presendFromI = 0
				m.compose.to.SetValue(to)
				m.compose.cc.SetValue(cc)
				m.compose.bcc.SetValue(bcc)
				m.compose.subject.SetValue(subject)
				if cc != "" || bcc != "" {
					m.compose.extraVisible = true
				}
				m.compose.step = 3

				return m.launchEditorWithBodyCmd(to, cc, subject, body)
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
	lower := strings.ToLower(text)
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
		lower := strings.ToLower(text)
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
	if len(matches) > 1 || text == "" {
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
