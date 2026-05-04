package notify

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/screener"
)

func newScreener(t *testing.T, notifyEntries []string) *screener.Screener {
	t.Helper()
	dir := t.TempDir()
	notifyPath := filepath.Join(dir, "notify.txt")
	if len(notifyEntries) > 0 {
		body := ""
		for _, e := range notifyEntries {
			body += e + "\n"
		}
		if err := os.WriteFile(notifyPath, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sc, err := screener.New(screener.Config{
		ScreenedIn:  filepath.Join(dir, "in.txt"),
		ScreenedOut: filepath.Join(dir, "out.txt"),
		Feed:        filepath.Join(dir, "feed.txt"),
		PaperTrail:  filepath.Join(dir, "pt.txt"),
		Spam:        filepath.Join(dir, "spam.txt"),
		Notify:      notifyPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func TestNotifier_DisabledIsNoop(t *testing.T) {
	n := New(config.NotificationsConfig{Enabled: false})
	if n.Enabled() {
		t.Error("expected Enabled() = false")
	}
	if err := n.Send("title", "body"); err != nil {
		t.Errorf("Send when disabled returned %v, want nil", err)
	}
}

func TestNotifier_ResolvedDefaults(t *testing.T) {
	n := New(config.NotificationsConfig{Enabled: true})
	if n.cfg.Command != "notify-send" {
		t.Errorf("Command default = %q, want notify-send", n.cfg.Command)
	}
	if n.cfg.Icon != "mail-message-new" {
		t.Errorf("Icon default = %q", n.cfg.Icon)
	}
	if n.cfg.ExpireMs != 5000 {
		t.Errorf("ExpireMs default = %d", n.cfg.ExpireMs)
	}
	if len(n.cfg.Folders) != 1 || n.cfg.Folders[0] != "Inbox" {
		t.Errorf("Folders default = %v", n.cfg.Folders)
	}
}

func TestMaybeNotify_FirstRunBaselineSilent(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	state := LoadState(statePath)
	sc := newScreener(t, []string{"vip@example.com"})
	// Use a fake command that always succeeds so we'd notice if it was invoked.
	n := New(config.NotificationsConfig{Enabled: true, Command: "true", Folders: []string{"Inbox"}})
	emails := []imap.Email{
		{UID: 100, From: "vip@example.com", Subject: "hi"},
		{UID: 101, From: "other@example.com", Subject: "hello"},
	}
	res := n.MaybeNotify("acct", "Inbox", emails, nil, sc, state)
	if res.Sent != 0 {
		t.Errorf("first run sent = %d, want 0 (baseline-only pass)", res.Sent)
	}
	uid, ok := state.Get(stateKey("acct", "Inbox"))
	if !ok || uid != 101 {
		t.Errorf("baseline = (%d, %v), want (101, true)", uid, ok)
	}
}

func TestMaybeNotify_OnlyNewEmailsFromNotifyList(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	state := LoadState(statePath)
	state.Set(stateKey("acct", "Inbox"), 100)

	sc := newScreener(t, []string{"vip@example.com"})
	n := New(config.NotificationsConfig{Enabled: true, Command: "true", Folders: []string{"Inbox"}})

	emails := []imap.Email{
		{UID: 100, From: "vip@example.com", Subject: "old"},     // not new
		{UID: 101, From: "vip@example.com", Subject: "new!"},    // new + on notify list
		{UID: 102, From: "other@example.com", Subject: "noise"}, // new but not on notify list
	}
	res := n.MaybeNotify("acct", "Inbox", emails, nil, sc, state)
	if res.Sent != 1 {
		t.Errorf("sent = %d, want 1", res.Sent)
	}
}

func TestMaybeNotify_DomainEntry(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	state := LoadState(statePath)
	state.Set(stateKey("acct", "Inbox"), 0)

	sc := newScreener(t, []string{"@important.org"})
	n := New(config.NotificationsConfig{Enabled: true, Command: "true", Folders: []string{"Inbox"}})

	emails := []imap.Email{
		{UID: 1, From: "alice@important.org", Subject: "x"},
		{UID: 2, From: "bob@important.org", Subject: "y"},
		{UID: 3, From: "spam@nowhere.com", Subject: "z"},
	}
	res := n.MaybeNotify("acct", "Inbox", emails, nil, sc, state)
	if res.Sent != 2 {
		t.Errorf("sent = %d, want 2 (both @important.org senders)", res.Sent)
	}
}

func TestMaybeNotify_FolderAllowlistFiltersOut(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	state := LoadState(statePath)
	state.Set(stateKey("acct", "Inbox"), 0)

	sc := newScreener(t, []string{"vip@example.com"})
	n := New(config.NotificationsConfig{Enabled: true, Command: "true", Folders: []string{"Inbox"}})

	emails := []imap.Email{
		{UID: 5, From: "vip@example.com", Subject: "moved-to-feed"},
	}
	// Email is auto-screened to Feed → allowlist excludes it.
	dst := map[uint32]string{5: "Feed"}
	res := n.MaybeNotify("acct", "Inbox", emails, dst, sc, state)
	if res.Sent != 0 {
		t.Errorf("sent = %d, want 0 (Feed not in allowlist)", res.Sent)
	}
}

func TestState_PersistAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := LoadState(path)
	state.Set("acct|Inbox", 42)
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded := LoadState(path)
	uid, ok := reloaded.Get("acct|Inbox")
	if !ok || uid != 42 {
		t.Errorf("reloaded = (%d, %v), want (42, true)", uid, ok)
	}
}

func TestState_MissingFileReturnsEmpty(t *testing.T) {
	state := LoadState(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if state == nil {
		t.Fatal("LoadState should never return nil")
	}
	if state.UIDs == nil {
		t.Error("UIDs map should be initialised")
	}
	if _, ok := state.Get("anything"); ok {
		t.Error("expected no entries")
	}
}
