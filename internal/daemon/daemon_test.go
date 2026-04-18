package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/screener"
)

func TestNew(t *testing.T) {
	cfg := config.Config{
		UI: config.UIConfig{
			BgSyncInterval: 5,
		},
		Folders: config.FoldersConfig{
			Inbox:       "INBOX",
			ScreenedOut: "ScreenedOut",
			Feed:        "Feed",
			PaperTrail:  "PaperTrail",
			Spam:        "Spam",
			Trash:       "Trash",
		},
	}

	imapCli := &imap.Client{}
	sc := &screener.Screener{}

	d := New(cfg, imapCli, sc)

	if d == nil {
		t.Fatal("New() returned nil")
	}
	if d.cfg.UI.BgSyncInterval != 5 {
		t.Errorf("expected BgSyncInterval=5, got %d", d.cfg.UI.BgSyncInterval)
	}
	if d.imapCli != imapCli {
		t.Error("IMAP client not set correctly")
	}
	if d.screener != sc {
		t.Error("Screener not set correctly")
	}
	if d.logger == nil {
		t.Error("Logger not initialized")
	}
}

func TestRun_InvalidInterval(t *testing.T) {
	cfg := config.Config{
		UI: config.UIConfig{
			BgSyncInterval: 0, // Invalid for daemon mode
		},
	}

	d := New(cfg, &imap.Client{}, &screener.Screener{})

	err := d.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for bg_sync_interval=0, got nil")
	}
	if err.Error() != "bg_sync_interval must be > 0 for daemon mode (got 0)" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// Note: Full integration test of Run() requires a real IMAP server
// This is tested manually and in integration tests

func TestReloadScreener(t *testing.T) {
	tmpDir := t.TempDir()
	listDir := filepath.Join(tmpDir, "lists")
	if err := os.MkdirAll(listDir, 0755); err != nil {
		t.Fatal(err)
	}

	screenedInPath := filepath.Join(listDir, "screened_in.txt")
	if err := os.WriteFile(screenedInPath, []byte("test@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"screened_out.txt", "feed.txt", "papertrail.txt", "spam.txt"} {
		if err := os.WriteFile(filepath.Join(listDir, name), []byte{}, 0600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Config{
		UI: config.UIConfig{
			BgSyncInterval: 5,
		},
		Screener: config.ScreenerConfig{
			ScreenedIn:  screenedInPath,
			ScreenedOut: filepath.Join(listDir, "screened_out.txt"),
			Feed:        filepath.Join(listDir, "feed.txt"),
			PaperTrail:  filepath.Join(listDir, "papertrail.txt"),
			Spam:        filepath.Join(listDir, "spam.txt"),
		},
	}

	sc, err := screener.New(screener.Config{
		ScreenedIn:  cfg.Screener.ScreenedIn,
		ScreenedOut: cfg.Screener.ScreenedOut,
		Feed:        cfg.Screener.Feed,
		PaperTrail:  cfg.Screener.PaperTrail,
		Spam:        cfg.Screener.Spam,
	})
	if err != nil {
		t.Fatal(err)
	}

	d := New(cfg, &imap.Client{}, sc)

	// Add another email to the list
	if err := os.WriteFile(screenedInPath, []byte("test@example.com\nnew@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Reload screener
	if err := d.reloadScreener(); err != nil {
		t.Fatalf("reloadScreener failed: %v", err)
	}

	// Verify new screener has both emails
	if d.screener.Classify("new@example.com") != screener.CategoryInbox {
		t.Error("reloaded screener should classify new@example.com as Inbox")
	}
}
