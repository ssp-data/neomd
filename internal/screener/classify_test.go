package screener

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/imap"
)

func TestClassifyForScreen(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test screener lists
	screenedInPath := filepath.Join(tmpDir, "screened_in.txt")
	screenedOutPath := filepath.Join(tmpDir, "screened_out.txt")
	feedPath := filepath.Join(tmpDir, "feed.txt")
	papertrailPath := filepath.Join(tmpDir, "papertrail.txt")
	spamPath := filepath.Join(tmpDir, "spam.txt")

	if err := os.WriteFile(screenedInPath, []byte("approved@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(screenedOutPath, []byte("blocked@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(feedPath, []byte("newsletter@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(papertrailPath, []byte("receipts@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spamPath, []byte("spam@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}

	sc, err := New(Config{
		ScreenedIn:  screenedInPath,
		ScreenedOut: screenedOutPath,
		Feed:        feedPath,
		PaperTrail:  papertrailPath,
		Spam:        spamPath,
	})
	if err != nil {
		t.Fatal(err)
	}

	folderCfg := config.FoldersConfig{
		Inbox:       "INBOX",
		ToScreen:    "ToScreen",
		ScreenedOut: "ScreenedOut",
		Feed:        "Feed",
		PaperTrail:  "PaperTrail",
		Spam:        "Spam",
		Trash:       "Trash",
	}

	tests := []struct {
		name          string
		emails        []imap.Email
		expectedMoves int
		expectedDsts  []string
	}{
		{
			name: "screened out email",
			emails: []imap.Email{
				{UID: 1, From: "blocked@example.com", Subject: "Test"},
			},
			expectedMoves: 1,
			expectedDsts:  []string{"ScreenedOut"},
		},
		{
			name: "feed email",
			emails: []imap.Email{
				{UID: 2, From: "newsletter@example.com", Subject: "News"},
			},
			expectedMoves: 1,
			expectedDsts:  []string{"Feed"},
		},
		{
			name: "papertrail email",
			emails: []imap.Email{
				{UID: 3, From: "receipts@example.com", Subject: "Receipt"},
			},
			expectedMoves: 1,
			expectedDsts:  []string{"PaperTrail"},
		},
		{
			name: "spam email",
			emails: []imap.Email{
				{UID: 4, From: "spam@example.com", Subject: "Spam"},
			},
			expectedMoves: 1,
			expectedDsts:  []string{"Spam"},
		},
		{
			name: "approved email (stays in inbox)",
			emails: []imap.Email{
				{UID: 5, From: "approved@example.com", Subject: "Good"},
			},
			expectedMoves: 0,
			expectedDsts:  []string{},
		},
		{
			name: "unknown sender (moves to ToScreen)",
			emails: []imap.Email{
				{UID: 6, From: "unknown@example.com", Subject: "Unknown"},
			},
			expectedMoves: 1,
			expectedDsts:  []string{"ToScreen"},
		},
		{
			name: "mixed batch",
			emails: []imap.Email{
				{UID: 7, From: "blocked@example.com", Subject: "1"},
				{UID: 8, From: "approved@example.com", Subject: "2"},
				{UID: 9, From: "newsletter@example.com", Subject: "3"},
				{UID: 10, From: "spam@example.com", Subject: "4"},
				{UID: 11, From: "unknown@example.com", Subject: "5"},
			},
			expectedMoves: 4,
			expectedDsts:  []string{"ScreenedOut", "Feed", "Spam", "ToScreen"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			moves, err := ClassifyForScreen(sc, tt.emails, folderCfg)
			if err != nil {
				t.Fatalf("ClassifyForScreen failed: %v", err)
			}

			if len(moves) != tt.expectedMoves {
				t.Errorf("expected %d moves, got %d", tt.expectedMoves, len(moves))
			}

			for i, mv := range moves {
				if i >= len(tt.expectedDsts) {
					t.Errorf("unexpected move %d: %s -> %s", i, mv.Email.From, mv.Dst)
					continue
				}
				if mv.Dst != tt.expectedDsts[i] {
					t.Errorf("move %d: expected dst=%s, got %s", i, tt.expectedDsts[i], mv.Dst)
				}
			}
		})
	}
}

func TestValidateScreenerSafety(t *testing.T) {
	tests := []struct {
		name        string
		folderCfg   config.FoldersConfig
		expectError bool
	}{
		{
			name: "safe config",
			folderCfg: config.FoldersConfig{
				Inbox:       "INBOX",
				ToScreen:    "ToScreen",
				ScreenedOut: "ScreenedOut",
				Feed:        "Feed",
				PaperTrail:  "PaperTrail",
				Spam:        "Spam",
				Trash:       "Trash",
			},
			expectError: false,
		},
		{
			name: "ToScreen points to Trash",
			folderCfg: config.FoldersConfig{
				ToScreen: "Trash",
				Trash:    "Trash",
			},
			expectError: true,
		},
		{
			name: "ScreenedOut points to Trash",
			folderCfg: config.FoldersConfig{
				ScreenedOut: "Trash",
				Trash:       "Trash",
			},
			expectError: true,
		},
		{
			name: "Feed points to Trash",
			folderCfg: config.FoldersConfig{
				Feed:  "Trash",
				Trash: "Trash",
			},
			expectError: true,
		},
		{
			name: "PaperTrail points to Trash",
			folderCfg: config.FoldersConfig{
				PaperTrail: "Trash",
				Trash:      "Trash",
			},
			expectError: true,
		},
		{
			name: "Spam points to Trash",
			folderCfg: config.FoldersConfig{
				Spam:  "Trash",
				Trash: "Trash",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateScreenerSafety(tt.folderCfg)
			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

func TestClassifyForScreen_EmptyInbox(t *testing.T) {
	tmpDir := t.TempDir()

	screenedInPath := filepath.Join(tmpDir, "screened_in.txt")
	if err := os.WriteFile(screenedInPath, []byte("test@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"screened_out.txt", "feed.txt", "papertrail.txt", "spam.txt"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte{}, 0600); err != nil {
			t.Fatal(err)
		}
	}

	sc, err := New(Config{
		ScreenedIn:  screenedInPath,
		ScreenedOut: filepath.Join(tmpDir, "screened_out.txt"),
		Feed:        filepath.Join(tmpDir, "feed.txt"),
		PaperTrail:  filepath.Join(tmpDir, "papertrail.txt"),
		Spam:        filepath.Join(tmpDir, "spam.txt"),
	})
	if err != nil {
		t.Fatal(err)
	}

	folderCfg := config.FoldersConfig{
		Inbox:       "INBOX",
		ToScreen:    "ToScreen",
		ScreenedOut: "ScreenedOut",
		Feed:        "Feed",
		PaperTrail:  "PaperTrail",
		Spam:        "Spam",
		Trash:       "Trash",
	}

	// Empty inbox should return no moves
	moves, err := ClassifyForScreen(sc, []imap.Email{}, folderCfg)
	if err != nil {
		t.Fatalf("ClassifyForScreen failed: %v", err)
	}

	if len(moves) != 0 {
		t.Errorf("expected 0 moves for empty inbox, got %d", len(moves))
	}
}
