package ui

// Private per-email notes (space n): the popup opens on the cursor email and
// closes with <space>n / esc; the editor result lands in the local store at
// once (✎ snippet in the row, Note line in the reader header) with the
// sidecar APPEND behind the list; an emptied note deletes; server notes
// merge in; [notes] enabled = false leaves everything unwired; rows with a
// snippet never overflow the width.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/notes"
)

// notesTestModel wraps instantModel with the notes machinery enabled: a temp
// store wired to the snippet pipeline for account "me@x".
func notesTestModel(t *testing.T, n int) Model {
	t.Helper()
	m := instantModel(t, n)
	m.cfg.Notes = config.NotesConfig{Enabled: true, Folder: "neomd/Notes"}
	store, err := notes.Load(filepath.Join(t.TempDir(), "notes"))
	if err != nil {
		t.Fatal(err)
	}
	m.notes = notesModel{store: store, folder: "neomd/Notes", fetched: map[string]bool{}}
	prevSrc, prevAcc := noteSnippetSource, tagActiveAccount
	tagActiveAccount = "me@x"
	noteSnippetSource = func(id string) (notes.Note, bool) { return store.Get(tagActiveAccount, id) }
	t.Cleanup(func() { noteSnippetSource, tagActiveAccount = prevSrc, prevAcc })
	return m
}

func openNotePopup(t *testing.T, m Model) Model {
	t.Helper()
	res, _ := m.handleChord(" ", "n")
	mm := res.(Model)
	if mm.state != stateNote {
		t.Fatalf("state = %v, want stateNote (status %q)", mm.state, mm.status)
	}
	return mm
}

func TestNotes_PopupOpensOnCursorAndSpaceNCloses(t *testing.T) {
	m := notesTestModel(t, 3)
	m.inbox.Select(1) // cursor on uid 2
	mm := openNotePopup(t, m)
	if mm.notes.target.UID != 2 {
		t.Fatalf("target = uid %d, want 2", mm.notes.target.UID)
	}
	if !strings.Contains(stripANSI(mm.View()), "no note yet") {
		t.Errorf("empty popup must say so: %q", stripANSI(mm.View()))
	}
	// <space>n again closes — the chord that opened it.
	res, _ := mm.updateNote(key(" "))
	mm = res.(Model)
	if mm.state != stateNote || !mm.notes.pendingSpace {
		t.Fatal("bare space must wait for the chord key")
	}
	res, _ = mm.updateNote(key("n"))
	mm = res.(Model)
	if mm.state != stateInbox {
		t.Errorf("<space>n must close the popup, state = %v", mm.state)
	}
	// esc closes too.
	mm = openNotePopup(t, m)
	res, _ = mm.updateNote(key("esc"))
	if res.(Model).state != stateInbox {
		t.Error("esc must close the popup")
	}
}

func TestNotes_NoMessageIDRefused(t *testing.T) {
	m := notesTestModel(t, 1)
	m.emails[0].MessageID = ""
	m.applyFilter()
	res, _ := m.handleChord(" ", "n")
	mm := res.(Model)
	if mm.state == stateNote || !strings.Contains(mm.status, "Message-ID") {
		t.Errorf("an email without Message-ID cannot carry a note: state=%v status=%q", mm.state, mm.status)
	}
}

func TestNotes_EditorDoneSavesLocallyAndRendersSnippet(t *testing.T) {
	m := notesTestModel(t, 2)
	m.clients = []*imap.Client{nil} // no IMAP: local-only path, no goroutine
	target := m.emails[0]
	res, _ := m.Update(noteEditorDoneMsg{account: "P", registryKey: "me@x", target: target, body: "have replied\nsecond line"})
	mm := res.(Model)
	n, ok := mm.notes.store.Get("me@x", target.MessageID)
	if !ok || n.Body != "have replied\nsecond line" {
		t.Fatalf("note not in store: %+v ok=%v", n, ok)
	}
	if !strings.Contains(mm.status, "locally") {
		t.Errorf("status = %q, want the local-only notice", mm.status)
	}
	// Row: first line after the subject, muted, with the ✎ marker.
	row := stripANSI(renderRow(listItemAt(mm, target.UID), 120))
	if !strings.Contains(row, "✎ have replied") {
		t.Errorf("row must show the snippet: %q", row)
	}
	if strings.Contains(row, "second line") {
		t.Errorf("row must show the first line only: %q", row)
	}
	// Other rows unaffected.
	if other := stripANSI(renderRow(listItemAt(mm, 1), 120)); strings.Contains(other, "✎") {
		t.Errorf("note leaked onto another row: %q", other)
	}
	// Reader header: the full text.
	hdr := stripANSI(renderEmailHeader(&target, nil, imap.SpyPixelInfo{}, 120))
	if !strings.Contains(hdr, "Note:") || !strings.Contains(hdr, "have replied") || !strings.Contains(hdr, "second line") {
		t.Errorf("reader header must carry the whole note:\n%s", hdr)
	}
	// Popup shows the full note too.
	mm = openNotePopup(t, mm)
	if v := stripANSI(mm.View()); !strings.Contains(v, "have replied") || !strings.Contains(v, "second line") {
		t.Errorf("popup must show the note: %q", v)
	}
}

func TestNotes_LocalFileWrittenThrough(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	store, _ := notes.Load(dir)
	m := notesTestModel(t, 1)
	m.notes.store = store
	m.clients = []*imap.Client{nil}
	target := m.emails[0]
	res, _ := m.Update(noteEditorDoneMsg{account: "P", registryKey: "me@x", target: target, body: "the book email"})
	_ = res
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s2, _ := notes.Load(dir)
		if n, ok := s2.Get("me@x", target.MessageID); ok && n.Body == "the book email" {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("note file never landed on disk")
}

func TestNotes_UnchangedEditorIsNoop(t *testing.T) {
	m := notesTestModel(t, 1)
	m.clients = []*imap.Client{nil}
	res, _ := m.Update(noteEditorDoneMsg{account: "P", registryKey: "me@x", target: m.emails[0], unchanged: true})
	mm := res.(Model)
	if _, ok := mm.notes.store.Get("me@x", m.emails[0].MessageID); ok || mm.status != "" {
		t.Errorf("unchanged editor must not create a note (status %q)", mm.status)
	}
}

func TestNotes_EmptyBodyDeletes(t *testing.T) {
	m := notesTestModel(t, 1)
	m.clients = []*imap.Client{nil}
	target := m.emails[0]
	m.notes.store.Set("me@x", notes.Note{MessageID: target.MessageID, Body: "old"})
	res, _ := m.Update(noteEditorDoneMsg{account: "P", registryKey: "me@x", target: target, body: "  \n"})
	mm := res.(Model)
	if _, ok := mm.notes.store.Get("me@x", target.MessageID); ok {
		t.Error("an emptied note must be deleted")
	}
	if mm.status != "note deleted" {
		t.Errorf("status = %q", mm.status)
	}
	if row := stripANSI(renderRow(listItemAt(mm, target.UID), 120)); strings.Contains(row, "✎") {
		t.Errorf("snippet must disappear: %q", row)
	}
}

func TestNotes_PopupDeleteAsksThenDeletes(t *testing.T) {
	m := notesTestModel(t, 1)
	m.clients = []*imap.Client{nil}
	m.notes.store.Set("me@x", notes.Note{MessageID: m.emails[0].MessageID, Body: "x"})
	mm := openNotePopup(t, m)
	res, _ := mm.updateNote(key("d"))
	mm = res.(Model)
	if !mm.notes.confirmDelete || !strings.Contains(stripANSI(mm.View()), "y/n") {
		t.Fatal("d must ask y/n first")
	}
	res, _ = mm.updateNote(key("n"))
	mm = res.(Model)
	if _, ok := mm.notes.store.Get("me@x", m.emails[0].MessageID); !ok {
		t.Fatal("n must keep the note")
	}
	res, _ = mm.updateNote(key("d"))
	res, _ = res.(Model).updateNote(key("y"))
	mm = res.(Model)
	if _, ok := mm.notes.store.Get("me@x", m.emails[0].MessageID); ok || mm.state != stateInbox {
		t.Errorf("y must delete and close: state=%v", mm.state)
	}
}

func TestNotes_ServerLoadMergesIntoStore(t *testing.T) {
	m := notesTestModel(t, 2)
	m.notes.store.Set("me@x", notes.Note{MessageID: m.emails[1].MessageID, Body: "local only"})
	res, _ := m.Update(notesLoadedMsg{registryKey: "me@x", notes: []notes.Note{
		{MessageID: m.emails[0].MessageID, UID: 9, Body: "from another machine"},
	}})
	mm := res.(Model)
	if n, ok := mm.notes.store.Get("me@x", m.emails[0].MessageID); !ok || n.UID != 9 {
		t.Errorf("server note missing: %+v", n)
	}
	if _, ok := mm.notes.store.Get("me@x", m.emails[1].MessageID); !ok {
		t.Error("local-only note must survive a server merge")
	}
	if row := stripANSI(renderRow(listItemAt(mm, m.emails[0].UID), 120)); !strings.Contains(row, "✎ from another machine") {
		t.Errorf("merged note must render: %q", row)
	}
	// A failed fetch is silent and changes nothing.
	res, _ = mm.Update(notesLoadedMsg{registryKey: "me@x", err: errors.New("boom")})
	if mm2 := res.(Model); mm2.isError || len(mm2.notes.store.All("me@x")) != 2 {
		t.Error("fetch error must be silent")
	}
}

func TestNotes_SavedMsgRecordsUIDAndErrorKeepsLocal(t *testing.T) {
	m := notesTestModel(t, 1)
	id := m.emails[0].MessageID
	m.notes.store.Set("me@x", notes.Note{MessageID: id, Body: "x"})
	res, _ := m.Update(noteSavedMsg{registryKey: "me@x", messageID: id, uid: 42})
	mm := res.(Model)
	if n, _ := mm.notes.store.Get("me@x", id); n.UID != 42 {
		t.Errorf("UID = %d, want 42", n.UID)
	}
	res, _ = mm.Update(noteSavedMsg{registryKey: "me@x", messageID: id, err: errors.New("APPEND failed")})
	mm = res.(Model)
	if !mm.isError || !strings.Contains(mm.status, "kept locally") {
		t.Errorf("server error must keep the local copy and say so: %q", mm.status)
	}
	if _, ok := mm.notes.store.Get("me@x", id); !ok {
		t.Error("note vanished on server error")
	}
}

func TestNotes_RowSnippetWidthAccounting(t *testing.T) {
	m := notesTestModel(t, 1)
	m.notes.store.Set("me@x", notes.Note{MessageID: m.emails[0].MessageID,
		Body: "a very long note that goes on and on about the book email and more and more words"})
	m.emails[0].Subject = "A fairly long subject line that also wants its space in the row"
	m.applyFilter()
	item := listItemAt(m, m.emails[0].UID)
	plain := emailItem{email: m.emails[0], index: item.index}
	plain.email.MessageID = "<no-note@x>"
	for _, w := range []int{190, 120, 80, 70, 60} {
		row := stripANSI(renderRow(item, w))
		if got := runewidth.StringWidth(row); got > w {
			t.Errorf("width=%d: row is %d cells: %q", w, got, row)
		}
		base := runewidth.StringWidth(stripANSI(renderRow(plain, w)))
		if got := runewidth.StringWidth(row); got != base {
			t.Errorf("width=%d: note row %d cells, plain row %d — must be identical", w, got, base)
		}
	}
	// Wide: the subject keeps the lead (snippet capped at half the budget).
	row := stripANSI(renderRow(item, 120))
	if !strings.Contains(row, "✎ a very long note") || !strings.Contains(row, "A fairly long subject") {
		t.Errorf("width=120 must show subject and snippet: %q", row)
	}
}

func TestNotes_DisabledConfigIsKillSwitch(t *testing.T) {
	prev := noteSnippetSource
	defer func() { noteSnippetSource = prev }()
	noteSnippetSource = nil
	cfg := &config.Config{NotesDir: t.TempDir(), Notes: config.NotesConfig{Enabled: false}}
	nm, notice := newNotesModel(cfg)
	if notice != "" || nm.store != nil || noteSnippetSource != nil {
		t.Fatal("disabled: nothing may be wired")
	}
	m := instantModel(t, 1)
	m.cfg.Notes.Enabled = false
	res, _ := m.handleChord(" ", "n")
	mm := res.(Model)
	if mm.state == stateNote || !strings.Contains(mm.status, "disabled") {
		t.Errorf("chord must be inert: state=%v status=%q", mm.state, mm.status)
	}
	if s, w := noteRowSnippet("<m1@x>", 60); s != "" || w != 0 {
		t.Error("no pipeline, no snippet")
	}
	// Enabled wires it.
	cfg.Notes.Enabled = true
	nm, _ = newNotesModel(cfg)
	if nm.store == nil || noteSnippetSource == nil || nm.folder != "neomd/Notes" {
		t.Error("enabled must wire the store, the pipeline and the default folder")
	}
}

func TestTagsPicker_SpaceTClosesEnterToggles(t *testing.T) {
	m := tagsTestModel(t, 1)
	m.tags.store.Add("me@x", "work", "<x@x>")
	mm := openPicker(t, m)
	// A bare space no longer toggles.
	res, cmd := mm.updateTags(key(" "))
	mm = res.(Model)
	if cmd != nil || len(keywordsOf(mm, 1)) != 0 {
		t.Fatal("space must not toggle a tag any more")
	}
	res, _ = mm.updateTags(key("t"))
	mm = res.(Model)
	if mm.state != stateInbox {
		t.Errorf("<space>t must close the picker, state = %v", mm.state)
	}
	mm = openPicker(t, m)
	res, cmd = mm.updateTags(key("enter"))
	mm = res.(Model)
	if cmd == nil || len(keywordsOf(mm, 1)) != 1 {
		t.Errorf("enter must toggle: keywords %v", keywordsOf(mm, 1))
	}
	if !strings.Contains(stripANSI(mm.viewTags()), "enter toggle") {
		t.Error("picker help must name enter as the toggle")
	}
}
