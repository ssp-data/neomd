package ui

// ── Private per-email notes (space n) ────────────────────────────────────
//
// A note is a free-text Markdown comment on one email, visible only to you
// (HEY "thread notes"). Server copy: a small sidecar message in [notes]
// folder (internal/imap/notes.go) keyed by the email's Message-ID — the
// email itself is never modified. Local copy: <config dir>/notes/ (one file
// per note, internal/notes), written through on every save so the ✎ snippet
// shows before the first server fetch lands. Everything that touches the
// network runs in a tea.Cmd: the popup opens from the local map, a save
// updates the map first and APPENDs behind the list, a fetch merges when it
// arrives. Keyed per account (the account's bare From address, like tags).

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/editor"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/notes"
)

// noteSnippetSource feeds the inbox row renderer and the reader header with
// the note of one Message-ID (package-level, like tagKeywordSource: the list
// delegate renders rows without the Model). nil = feature off / tests. The
// closure reads tagActiveAccount — the same per-account key tags use.
var noteSnippetSource func(messageID string) (notes.Note, bool)

// notesModel is the whole notes-feature state on Model (one field, notes).
type notesModel struct {
	store  *notes.Store
	folder string
	// target is the email whose note the popup shows (captured by value
	// when the popup opens).
	target imap.Email
	// pendingSpace is set after a bare <space> inside the popup so that
	// <space>n closes it (the same chord that opened it).
	pendingSpace bool
	// confirmDelete asks y/n after d.
	confirmDelete bool
	// fetched remembers which accounts already pulled the server copy this
	// session (refreshed on every bg sync tick).
	fetched map[string]bool
}

// noteEditorDoneMsg is the $EDITOR result for one note: the new body (""
// = delete), or err / unchanged.
type noteEditorDoneMsg struct {
	account     string // account name (IMAP client lookup)
	registryKey string // account's bare address (store key)
	target      imap.Email
	body        string
	unchanged   bool
	err         error
}

// noteSavedMsg reports the sidecar APPEND (and old-sidecar expunge).
type noteSavedMsg struct {
	registryKey string
	messageID   string
	uid         uint32
	deleted     bool
	err         error
}

// notesLoadedMsg carries one account's server notes.
type notesLoadedMsg struct {
	registryKey string
	notes       []notes.Note
	err         error
}

// newNotesModel loads the local copy and wires the snippet pipeline; called
// once from New. [notes] enabled = false leaves the pipeline unwired (no
// snippet, no Note line, inert chord). Returns a startup notice or "".
func newNotesModel(cfg *config.Config) (notesModel, string) {
	if !cfg.Notes.Enabled {
		return notesModel{}, ""
	}
	store, err := notes.Load(cfg.NotesDir)
	notice := ""
	if err != nil {
		notice = "notes: " + err.Error()
		store = nil
	}
	noteSnippetSource = func(id string) (notes.Note, bool) { return store.Get(tagActiveAccount, id) }
	return notesModel{store: store, folder: cfg.Notes.NotesFolder(), fetched: map[string]bool{}}, notice
}

// noteFor returns the note of messageID for the active account from the
// package-level pipeline ("" id or feature off ⇒ none).
func noteFor(messageID string) (notes.Note, bool) {
	if noteSnippetSource == nil || messageID == "" {
		return notes.Note{}, false
	}
	return noteSnippetSource(messageID)
}

// notesEnabled reports whether the feature is on for this model.
func (m Model) notesEnabled() bool {
	return m.cfg == nil || m.cfg.Notes.Enabled
}

// openNote opens the note popup for the cursor email (inbox) or the open
// email (reader). Marked sets are ignored: a note belongs to one email.
func (m Model) openNote() (tea.Model, tea.Cmd) {
	if !m.notesEnabled() {
		m.status = "Notes are disabled ([notes] enabled = false in config.toml)."
		m.isError = false
		return m, nil
	}
	var target *imap.Email
	if m.state == stateReading && m.openEmail != nil {
		target = m.openEmail
	} else if e := m.currentEmail(); e != nil {
		target = e
	}
	if target == nil {
		m.status = "Notes: no email selected."
		m.isError = true
		return m, nil
	}
	if target.MessageID == "" {
		m.status = "Notes: this email has no Message-ID — nothing to attach a note to."
		m.isError = true
		return m, nil
	}
	m.prevState = m.state
	m.state = stateNote
	m.notes.target = *target
	m.notes.pendingSpace = false
	m.notes.confirmDelete = false
	return m, nil
}

// currentEmail is the cursor row's email in the inbox list, nil when empty.
func (m Model) currentEmail() *imap.Email { return selectedEmail(m.inbox) }

func (m Model) closeNote() (tea.Model, tea.Cmd) {
	m.state = m.prevState
	if m.state == stateNote {
		m.state = stateInbox
	}
	return m, nil
}

func (m Model) updateNote(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.notes.confirmDelete {
		m.notes.confirmDelete = false
		switch key {
		case "y", "Y":
			return m.deleteNote()
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}
	if m.notes.pendingSpace {
		m.notes.pendingSpace = false
		if key == "n" { // <space>n again closes, like it opened
			return m.closeNote()
		}
		return m, nil
	}
	switch key {
	case "esc", "q":
		return m.closeNote()
	case " ":
		m.notes.pendingSpace = true
		return m, nil
	case "e", "enter":
		return m.editNote()
	case "d":
		if _, ok := m.notes.store.Get(m.activeAccountKey(), m.notes.target.MessageID); ok {
			m.notes.confirmDelete = true
		}
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// editNote opens $EDITOR on the note body (temp neomd-note-*.md, body only —
// no prelude: the note is free text). On exit the body is read back; an
// unchanged file is a no-op, an emptied one deletes the note.
func (m Model) editNote() (tea.Model, tea.Cmd) {
	registryKey := m.activeAccountKey()
	account := m.activeAccountName()
	target := m.notes.target
	body := ""
	if n, ok := m.notes.store.Get(registryKey, target.MessageID); ok {
		body = n.Body
	}
	f, err := os.CreateTemp(neomdTempDir(), "neomd-note-*.md")
	if err != nil {
		m.status = "note: " + err.Error()
		m.isError = true
		return m, nil
	}
	tmpPath := f.Name()
	initial := body
	if initial != "" {
		initial += "\n"
	}
	f.WriteString(initial) //nolint
	f.Close()
	cmd := editor.Command(tmpPath)
	return m, tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		defer os.Remove(tmpPath)
		if execErr != nil {
			return noteEditorDoneMsg{account: account, registryKey: registryKey, target: target, err: execErr}
		}
		raw, readErr := os.ReadFile(tmpPath)
		if readErr != nil {
			return noteEditorDoneMsg{account: account, registryKey: registryKey, target: target, err: readErr}
		}
		got := strings.TrimRight(strings.ReplaceAll(string(raw), "\r\n", "\n"), " \t\n")
		if got == body {
			return noteEditorDoneMsg{account: account, registryKey: registryKey, target: target, unchanged: true}
		}
		return noteEditorDoneMsg{account: account, registryKey: registryKey, target: target, body: got}
	})
}

// handleNoteEditorDone applies the editor result: the local map and file
// first (the ✎ snippet changes at once), then the sidecar APPEND behind the
// list. Empty body = delete.
func (m Model) handleNoteEditorDone(msg noteEditorDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = "note: " + msg.err.Error()
		m.isError = true
		return m, nil
	}
	if msg.unchanged {
		return m, nil
	}
	if strings.TrimSpace(msg.body) == "" {
		if _, ok := m.notes.store.Get(msg.registryKey, msg.target.MessageID); !ok {
			return m, nil
		}
		return m.deleteNoteFor(msg.account, msg.registryKey, msg.target.MessageID)
	}
	prev, _ := m.notes.store.Get(msg.registryKey, msg.target.MessageID)
	n := notes.Note{MessageID: msg.target.MessageID, UID: prev.UID, Body: msg.body, Date: time.Now()}
	m.notes.store.Set(msg.registryKey, n)
	m.saveStoreAsync()
	m.refreshReaderNote(msg.target.MessageID)
	m.status = "note saved"
	m.isError = false
	cli := m.tagsClientFor(msg.account)
	if cli == nil {
		m.status = "note saved locally — account has no IMAP"
		return m, m.applyFilter()
	}
	raw := notes.Build(n, msg.registryKey, msg.target.Subject)
	folder := m.notes.folder
	replace := prev.UID
	key, id := msg.registryKey, msg.target.MessageID
	return m, tea.Batch(m.applyFilter(), func() tea.Msg {
		uid, err := cli.SaveNote(nil, folder, raw, replace)
		return noteSavedMsg{registryKey: key, messageID: id, uid: uid, err: err}
	})
}

// deleteNote (y after d in the popup) removes the note of the popup target.
func (m Model) deleteNote() (tea.Model, tea.Cmd) {
	mm, cmd := m.deleteNoteFor(m.activeAccountName(), m.activeAccountKey(), m.notes.target.MessageID)
	model := mm.(Model)
	model.state = model.prevState
	if model.state == stateNote {
		model.state = stateInbox
	}
	return model, cmd
}

func (m Model) deleteNoteFor(account, registryKey, messageID string) (tea.Model, tea.Cmd) {
	prev, ok := m.notes.store.Get(registryKey, messageID)
	if !ok {
		return m, nil
	}
	m.notes.store.Delete(registryKey, messageID)
	m.saveStoreAsync()
	m.refreshReaderNote(messageID)
	m.status = "note deleted"
	m.isError = false
	cli := m.tagsClientFor(account)
	if cli == nil || prev.UID == 0 {
		return m, m.applyFilter()
	}
	folder := m.notes.folder
	uid := prev.UID
	return m, tea.Batch(m.applyFilter(), func() tea.Msg {
		err := cli.DeleteNote(nil, folder, uid)
		return noteSavedMsg{registryKey: registryKey, messageID: messageID, deleted: true, err: err}
	})
}

// handleNoteSaved records the new sidecar UID (so the next edit replaces
// it) or reports a server error — the local copy stays either way.
func (m Model) handleNoteSaved(msg noteSavedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = fmt.Sprintf("note kept locally — server: %v", msg.err)
		m.isError = true
		return m, nil
	}
	if msg.deleted {
		return m, nil
	}
	if n, ok := m.notes.store.Get(msg.registryKey, msg.messageID); ok && n.UID != msg.uid {
		n.UID = msg.uid
		m.notes.store.Set(msg.registryKey, n)
		m.saveStoreAsync()
	}
	return m, nil
}

// fetchNotesCmd pulls the active account's sidecar folder in the background
// (startup, account switch, bg sync). nil when off or no IMAP.
func (m Model) fetchNotesCmd() tea.Cmd {
	if !m.notesEnabled() || m.notes.store == nil {
		return nil
	}
	cli := m.tagsClientFor(m.activeAccountName())
	if cli == nil {
		return nil
	}
	key := m.activeAccountKey()
	folder := m.notes.folder
	return func() tea.Msg {
		ns, err := cli.FetchNotes(nil, folder)
		return notesLoadedMsg{registryKey: key, notes: ns, err: err}
	}
}

// handleNotesLoaded merges the server copy into the local store (server
// wins per Message-ID, local-only notes survive) and refreshes the rows.
// Fetch errors are silent: the local copy is still shown.
func (m Model) handleNotesLoaded(msg notesLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		log.Printf("notes: fetch failed: %v", msg.err)
		return m, nil
	}
	if m.notes.fetched == nil {
		m.notes.fetched = map[string]bool{}
	}
	m.notes.fetched[msg.registryKey] = true
	m.notes.store.Merge(msg.registryKey, msg.notes)
	m.saveStoreAsync()
	return m, m.applyFilter()
}

// refreshReaderNote re-renders the open email's header when its note
// changed (the Note: block is part of the viewport content).
func (m *Model) refreshReaderNote(messageID string) {
	if m.openEmail == nil || m.openEmail.MessageID != messageID || m.openRawHeaders != "" {
		return
	}
	if m.cfg == nil {
		return
	}
	off := m.reader.YOffset
	_ = loadEmailIntoReader(&m.reader, m.openEmail, m.openBody, m.openAttachments, m.openSpyPixels, m.openLinks, glamourStyleFor(m.cfg.UI.Theme), m.width)
	m.reader.SetYOffset(off)
}

func (m Model) saveStoreAsync() {
	store := m.notes.store
	if store == nil {
		return
	}
	safeGo(func() {
		if err := store.Save(); err != nil {
			log.Printf("notes: saving local copy failed: %v", err)
		}
	})
}

// ── rendering ────────────────────────────────────────────────────────────

// noteMarker precedes the inbox snippet and the reader line.
const noteMarker = "✎ "

// noteRowSnippet returns the plain " ✎ first line" text for an inbox row and
// its width budget: at most half the subject column, so the subject keeps
// the lead. "" when the email has no note.
func noteRowSnippet(messageID string, subjectMax int) (string, int) {
	n, ok := noteFor(messageID)
	if !ok {
		return "", 0
	}
	first := n.FirstLine()
	if first == "" {
		return "", 0
	}
	budget := subjectMax / 2
	if budget < 6 {
		return "", 0
	}
	text := "  " + noteMarker + displaySafe(first)
	return truncate(text, budget), budget
}

// noteStyle is the muted snippet style (theme muted colour, italic).
func noteStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colorMuted).Italic(true)
}

// readerNoteLines renders the full note for the reader header (every line,
// the first behind "Note:", the rest indented), or nil.
func readerNoteLines(messageID string, width int) []string {
	n, ok := noteFor(messageID)
	if !ok || strings.TrimSpace(n.Body) == "" {
		return nil
	}
	max := width - 13
	if max < 8 {
		max = 8
	}
	var out []string
	for i, l := range strings.Split(n.Body, "\n") {
		l = truncate(displaySafe(l), max)
		if i == 0 {
			out = append(out, styleDate.Render("Note:    ")+noteStyle().Render(noteMarker+l))
			continue
		}
		out = append(out, "         "+noteStyle().Render(l))
	}
	return out
}

// viewNote is the popup: the full note (or an empty hint) with e / d / esc.
func (m Model) viewNote() string {
	w := 70
	t := m.notes.target
	title := "Note — " + truncate(displaySafe(t.Subject), w-12)
	lines := []string{styleHeader.Render(title), styleHelp.Render(" " + truncate(displaySafe(cleanFrom(t.From)), w-6)), ""}
	n, ok := m.notes.store.Get(m.activeAccountKey(), t.MessageID)
	if !ok || strings.TrimSpace(n.Body) == "" {
		lines = append(lines, styleHelp.Render("  no note yet — press e to write one"))
	} else {
		body := strings.Split(n.Body, "\n")
		const rows = 16
		for i, l := range body {
			if i == rows {
				lines = append(lines, styleHelp.Render(fmt.Sprintf("  … %d more line%s (e opens all)", len(body)-rows, plural(len(body)-rows))))
				break
			}
			lines = append(lines, "  "+truncate(displaySafe(l), w-8))
		}
		if !n.Date.IsZero() {
			lines = append(lines, "", styleHelp.Render("  edited "+fmtDateFull(n.Date)))
		}
	}
	if m.notes.confirmDelete {
		lines = append(lines, "", styleError.Render(" delete this note? · y/n"))
	}
	hint := " e edit · d delete · esc / <space>n close"
	if m.notes.pendingSpace {
		hint = " <space> n close"
	}
	lines = append(lines, "", styleHelp.Render(hint))
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(1, 2)
	box := boxStyle.Width(w).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
