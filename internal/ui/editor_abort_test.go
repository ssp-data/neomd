package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

const abortRaw = "# [neomd: to: a@example.com]\n# [neomd: subject: hi]\n\nhello there\n"

// ZZ without edits (or :q!) used to drop the whole compose session back to
// the inbox; the typed mail was only reachable via :recover. It must prompt.
func TestEditorAbortPromptsInsteadOfDroppingMessage(t *testing.T) {
	m := requeueTestModel()
	m.pendingIsReply = true
	m.attachments = []string{"/tmp/a.pdf"}
	mm, cmd := m.Update(editorDoneMsg{aborted: true, body: abortRaw})
	m = mm.(Model)
	if cmd != nil {
		t.Error("prompt must not run a command")
	}
	if m.state != stateCompose || !m.pendingDiscard || !m.reopenEditor {
		t.Fatalf("want compose + discard prompt, got state=%v pendingDiscard=%v reopenEditor=%v", m.state, m.pendingDiscard, m.reopenEditor)
	}
	if !strings.Contains(m.status, "without changes") || !strings.Contains(m.status, "n reopen editor") {
		t.Errorf("status should explain and offer reopen, got %q", m.status)
	}
	if !m.pendingIsReply || len(m.attachments) != 1 {
		t.Error("reply tracking and attachments must survive until the user decides")
	}
}

// An empty body is handled the same way, with its own wording.
func TestEditorEmptyBodyPrompts(t *testing.T) {
	m := requeueTestModel()
	mm, _ := m.Update(editorDoneMsg{body: "   \n"})
	m = mm.(Model)
	if m.state != stateCompose || !m.pendingDiscard || !m.reopenEditor {
		t.Fatalf("want compose + discard prompt, got state=%v pendingDiscard=%v", m.state, m.pendingDiscard)
	}
	if !strings.Contains(m.status, "empty body") {
		t.Errorf("status should mention the empty body, got %q", m.status)
	}
}

// n on the abort prompt reopens the editor on the file as it was.
func TestEditorAbortPromptNReopensEditor(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	m := requeueTestModel()
	mm, _ := m.Update(editorDoneMsg{aborted: true, body: abortRaw})
	m = mm.(Model)
	mm, cmd := m.Update(key("n"))
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("n must relaunch the editor")
	}
	if m.pendingDiscard || m.reopenEditor || m.reopenRaw != "" {
		t.Error("prompt state must be cleared once the editor is relaunched")
	}
	if m.state != stateCompose || m.status != "" {
		t.Errorf("want compose with empty status, got state=%v status=%q", m.state, m.status)
	}
}

// y on the abort prompt discards exactly like the esc-triggered prompt.
func TestEditorAbortPromptYDiscards(t *testing.T) {
	m := requeueTestModel()
	m.pendingIsReply = true
	m.attachments = []string{"/tmp/a.pdf"}
	mm, _ := m.Update(editorDoneMsg{aborted: true, body: abortRaw})
	m = mm.(Model)
	mm, cmd := m.Update(key("y"))
	m = mm.(Model)
	if cmd != nil {
		t.Error("discard must not run a command")
	}
	if m.state != stateInbox || m.pendingDiscard || m.reopenEditor {
		t.Errorf("want inbox with prompt cleared, got state=%v pendingDiscard=%v", m.state, m.pendingDiscard)
	}
	if m.pendingIsReply || m.attachments != nil {
		t.Error("discard must clear reply tracking and attachments")
	}
	if !strings.Contains(m.status, ":recover") {
		t.Errorf("discard status should point at :recover, got %q", m.status)
	}
}

// The esc-triggered prompt is unchanged: n keeps editing the form, no editor.
func TestEscDiscardPromptNStaysOnForm(t *testing.T) {
	m := requeueTestModel()
	m.state = stateCompose
	m.compose.to.SetValue("a@example.com")
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if !m.pendingDiscard || m.reopenEditor {
		t.Fatalf("esc should prompt without reopen flag, got pendingDiscard=%v reopenEditor=%v", m.pendingDiscard, m.reopenEditor)
	}
	mm, cmd := m.Update(key("n"))
	m = mm.(Model)
	if cmd != nil {
		t.Error("n on the esc prompt must not launch the editor")
	}
	if m.state != stateCompose || m.pendingDiscard || m.status != "" {
		t.Errorf("want compose form with cleared prompt, got state=%v pendingDiscard=%v status=%q", m.state, m.pendingDiscard, m.status)
	}
}
