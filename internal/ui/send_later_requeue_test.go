package ui

// Rescheduling a queued send-later message (E on it → l with a new time, or
// enter to send now) must replace the original queued copy — but only AFTER
// the replacement is safely stored or sent, and never when the session ends
// any other way (abort/discard/error): then the original stays untouched.

import (
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
	"time"

	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/imap"
)

func requeueTestModel() Model {
	cfg := &config.Config{
		Accounts: []config.AccountConfig{
			{Name: "Personal", User: "me@example.com", From: "Me <me@example.com>"},
		},
		Folders: config.FoldersConfig{Scheduled: "Scheduled", Trash: "Trash", Sent: "Sent"},
	}
	return Model{cfg: cfg, accounts: cfg.ActiveAccounts(), compose: newComposeModel()}
}

// E on a queued message remembers it for replacement; E on a regular email
// clears any stale requeue state.
func TestContinueDraftTracksQueuedOriginal(t *testing.T) {
	m := requeueTestModel()
	m.openEmail = &imap.Email{
		UID: 7, Folder: "Scheduled", Subject: "queued",
		SendAt: time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC),
	}
	mm, _ := m.continueDraft()
	m = mm.(Model)
	if m.requeue.uid != 7 || m.requeue.folder != "Scheduled" || m.requeue.account != "Personal" {
		t.Fatalf("requeue not tracked: %+v", m.requeue)
	}

	m.openEmail = &imap.Email{UID: 8, Folder: "INBOX", Subject: "regular email"}
	mm, _ = m.continueDraft()
	m = mm.(Model)
	if m.requeue.uid != 0 {
		t.Fatalf("regular email must clear requeue state: %+v", m.requeue)
	}
}

// Successful re-schedule fires the cleanup of the old copy and clears the
// tracking; a failed schedule keeps the old copy (no cleanup) — it is still
// the only valid version.
func TestScheduleDoneReplacesQueuedOriginal(t *testing.T) {
	m := requeueTestModel()
	m.requeue = requeueRef{uid: 7, folder: "Scheduled", account: "Personal"}

	mm, cmd := m.Update(scheduleDoneMsg{at: time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)})
	m = mm.(Model)
	if m.requeue.uid != 0 {
		t.Error("requeue not cleared after successful schedule")
	}
	if !strings.Contains(m.status, "Replaced the previous schedule") {
		t.Errorf("status = %q", m.status)
	}
	if cmd == nil {
		t.Fatal("no cleanup command fired")
	}
	if _, ok := cmd().(requeueCleanupDoneMsg); !ok {
		t.Fatal("cleanup command did not produce requeueCleanupDoneMsg")
	}

	// Failure path: old copy must survive (no cleanup command).
	m2 := requeueTestModel()
	m2.requeue = requeueRef{uid: 7, folder: "Scheduled", account: "Personal"}
	mm2, cmd2 := m2.Update(scheduleDoneMsg{err: errTest})
	m2 = mm2.(Model)
	if m2.requeue.uid != 0 {
		t.Error("requeue must be cleared on error too (stale state)")
	}
	if cmd2 != nil {
		t.Error("failed schedule must NOT delete the original queued copy")
	}
}

// Sending immediately (enter instead of l) also supersedes the queued copy.
func TestSendDoneReplacesQueuedOriginal(t *testing.T) {
	m := requeueTestModel()
	m.requeue = requeueRef{uid: 7, folder: "Scheduled", account: "Personal"}
	mm, cmd := m.Update(sendDoneMsg{})
	m = mm.(Model)
	if m.requeue.uid != 0 {
		t.Error("requeue not cleared after send")
	}
	if cmd == nil {
		t.Fatal("no cleanup command fired after send")
	}
	if _, ok := cmd().(requeueCleanupDoneMsg); !ok {
		t.Fatal("cleanup command did not produce requeueCleanupDoneMsg")
	}

	// Failed send: old queued copy stays.
	m2 := requeueTestModel()
	m2.requeue = requeueRef{uid: 7, folder: "Scheduled", account: "Personal"}
	_, cmd2 := m2.Update(sendDoneMsg{err: errTest})
	if cmd2 != nil {
		t.Error("failed send must NOT delete the original queued copy")
	}
}

// Aborting the editor keeps the original queued message untouched: the abort
// raises the discard prompt (requeue still tracked so "n" can continue), and
// confirming with y clears the tracking without deleting the queued copy.
func TestEditorAbortKeepsQueuedOriginal(t *testing.T) {
	m := requeueTestModel()
	m.requeue = requeueRef{uid: 7, folder: "Scheduled", account: "Personal"}
	mm, cmd := m.Update(editorDoneMsg{aborted: true})
	m = mm.(Model)
	if cmd != nil {
		t.Error("abort must NOT delete the original queued copy")
	}
	if !m.pendingDiscard || m.requeue.uid != 7 {
		t.Fatalf("abort should prompt and keep requeue; pendingDiscard=%v requeue=%+v", m.pendingDiscard, m.requeue)
	}
	mm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = mm.(Model)
	if m.requeue.uid != 0 {
		t.Error("requeue not cleared on confirmed discard")
	}
	if cmd != nil {
		t.Error("discard must NOT delete the original queued copy")
	}
}

// Cleanup failure must warn loudly — the daemon would deliver both copies.
func TestRequeueCleanupFailureWarns(t *testing.T) {
	m := requeueTestModel()
	mm, _ := m.Update(requeueCleanupDoneMsg{err: errTest})
	m = mm.(Model)
	if !m.isError || !strings.Contains(m.status, "OLD copy") {
		t.Errorf("cleanup failure not surfaced: %q", m.status)
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }

var errTest = testErr("boom")

// Re-saving a continued draft must replace the previous draft version, and a
// failed save must leave it untouched.
func TestSaveDraftReplacesPreviousVersion(t *testing.T) {
	m := requeueTestModel()
	m.cfg.Folders.Drafts = "Drafts"
	m.openEmail = &imap.Email{UID: 9, Folder: "Drafts", Subject: "wip"}
	mm, _ := m.continueDraft()
	m = mm.(Model)
	if m.requeue.uid != 9 || m.requeue.folder != "Drafts" {
		t.Fatalf("continued draft not tracked: %+v", m.requeue)
	}

	mm2, cmd := m.Update(saveDraftDoneMsg{})
	m = mm2.(Model)
	if m.requeue.uid != 0 {
		t.Error("requeue not cleared after draft save")
	}
	if !strings.Contains(m.status, "replaced the previous version") {
		t.Errorf("status = %q", m.status)
	}
	if cmd == nil {
		t.Fatal("no cleanup fired for the old draft version")
	}
	if _, ok := cmd().(requeueCleanupDoneMsg); !ok {
		t.Fatal("cleanup command did not produce requeueCleanupDoneMsg")
	}

	// Failed save: old draft stays, tracking cleared (no stale delete later).
	m3 := requeueTestModel()
	m3.requeue = requeueRef{uid: 9, folder: "Drafts", account: "Personal"}
	mm3, cmd3 := m3.Update(saveDraftDoneMsg{err: errTest})
	if cmd3 != nil {
		t.Error("failed save must NOT delete the previous draft")
	}
	if mm3.(Model).requeue.uid != 0 {
		t.Error("requeue must be cleared on save error (stale-delete protection)")
	}
}

// E on a regular email (not a draft, not queued) must never track it for
// deletion — sending a reply-like edit of a received mail must not trash it.
func TestContinueRegularEmailNeverTracked(t *testing.T) {
	m := requeueTestModel()
	m.cfg.Folders.Drafts = "Drafts"
	m.openEmail = &imap.Email{UID: 11, Folder: "INBOX", Subject: "received mail"}
	mm, _ := m.continueDraft()
	if mm.(Model).requeue.uid != 0 {
		t.Fatal("regular email tracked for deletion — would trash received mail on send")
	}
}

// The overdue watchdog counts only queued messages past due + grace — GTD
// mail without SendAt and not-yet-due queue entries never alarm.
func TestCountOverdueScheduled(t *testing.T) {
	now := time.Date(2026, 8, 24, 20, 0, 0, 0, time.UTC)
	emails := []imap.Email{
		{Subject: "gtd item"}, // no SendAt — never counted
		{Subject: "due future", SendAt: now.Add(2 * time.Hour)},  // not due
		{Subject: "just due", SendAt: now.Add(-5 * time.Minute)}, // within grace
		{Subject: "overdue", SendAt: now.Add(-30 * time.Minute)}, // counted
		{Subject: "very late", SendAt: now.Add(-48 * time.Hour)}, // counted
	}
	if got := countOverdueScheduled(emails, now); got != 2 {
		t.Errorf("overdue count = %d, want 2", got)
	}
	if got := countOverdueScheduled(nil, now); got != 0 {
		t.Errorf("empty folder: %d", got)
	}
}

// The overdue warning must reach the status bar loudly; zero overdue stays quiet.
func TestOverdueScheduledWarns(t *testing.T) {
	m := requeueTestModel()
	mm, _ := m.Update(overdueScheduledMsg{count: 2})
	m = mm.(Model)
	if !m.isError || !strings.Contains(m.status, "OVERDUE") {
		t.Errorf("overdue not surfaced: %q", m.status)
	}
	m2 := requeueTestModel()
	mm2, _ := m2.Update(overdueScheduledMsg{count: 0})
	if s := mm2.(Model).status; s != "" {
		t.Errorf("zero overdue must stay silent, got %q", s)
	}
}
