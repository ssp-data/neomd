package ui

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/imap"
)

func TestMaskEmail(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"user@example.com", "u***@example.com"},
		{"Name <user@example.com>", "Name <u***@example.com>"},
		{"a@b.com", "a***@b.com"},
		{"", ""},
		{"no-at-sign", "no-at-sign"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := maskEmail(tt.input)
			if got != tt.want {
				t.Errorf("maskEmail(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// isURLSchemeAllowed replicates the inline URL scheme check from model.go Update().
func isURLSchemeAllowed(url string) bool {
	lower := strings.ToLower(url)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func TestURLSchemeValidation(t *testing.T) {
	tests := []struct {
		url     string
		allowed bool
	}{
		{"http://example.com", true},
		{"https://example.com", true},
		{"HTTP://EXAMPLE.COM", true},
		{"https://secure.example.com/path?q=1", true},
		{"javascript:alert(1)", false},
		{"ftp://files.example.com", false},
		{"data:text/html,<h1>hi</h1>", false},
		{"", false},
		{"file:///etc/passwd", false},
		{"mailto:user@example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := isURLSchemeAllowed(tt.url)
			if got != tt.allowed {
				t.Errorf("isURLSchemeAllowed(%q) = %v, want %v", tt.url, got, tt.allowed)
			}
		})
	}
}

func TestMergeAutoBCC(t *testing.T) {
	tests := []struct {
		name    string
		bcc     string
		autoBCC string
		want    string
	}{
		{
			name:    "append when empty",
			bcc:     "",
			autoBCC: "archive@example.com",
			want:    "archive@example.com",
		},
		{
			name:    "append when distinct",
			bcc:     "team@example.com",
			autoBCC: "archive@example.com",
			want:    "team@example.com, archive@example.com",
		},
		{
			name:    "dedupe bare and named address",
			bcc:     "Archive <archive@example.com>",
			autoBCC: "archive@example.com",
			want:    "Archive <archive@example.com>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeAutoBCC(tt.bcc, tt.autoBCC); got != tt.want {
				t.Fatalf("mergeAutoBCC(%q, %q) = %q, want %q", tt.bcc, tt.autoBCC, got, tt.want)
			}
		})
	}
}

func TestCollectRcptTo(t *testing.T) {
	got := collectRcptTo(
		"Alice <alice@example.com>, bob@example.com",
		"bob@example.com, Carol <carol@example.com>",
		"alice@example.com, dave@example.com",
	)
	want := []string{"alice@example.com", "bob@example.com", "carol@example.com", "dave@example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collectRcptTo() = %#v, want %#v", got, want)
	}
}

func TestPresendSMTPAccount(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.AccountConfig{
			{Name: "Personal", From: "me@example.com"},
			{Name: "Work", From: "me@work.example"},
		},
		Senders: []config.SenderConfig{
			{Name: "Support", From: "support@example.com", Account: "Work"},
		},
	}
	m := Model{
		cfg:      cfg,
		accounts: cfg.ActiveAccounts(),
		accountI: 0,
	}

	t.Run("selected account uses its own SMTP account", func(t *testing.T) {
		m.presendFromI = 1
		if got := m.presendSMTPAccount().Name; got != "Work" {
			t.Fatalf("presendSMTPAccount() = %q, want %q", got, "Work")
		}
	})

	t.Run("sender alias resolves to referenced account", func(t *testing.T) {
		m.presendFromI = 2
		if got := m.presendSMTPAccount().Name; got != "Work" {
			t.Fatalf("presendSMTPAccount() = %q, want %q", got, "Work")
		}
	})
}

func TestSentDraftsIMAPClient_DefaultsToPrimaryAccount(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.AccountConfig{
			{Name: "Personal", From: "me@example.com"},
			{Name: "Work", From: "me@work.example"},
		},
	}
	personal := imap.New(imap.Config{Host: "personal"})
	work := imap.New(imap.Config{Host: "work"})
	m := Model{
		cfg:          cfg,
		accounts:     cfg.ActiveAccounts(),
		clients:      []*imap.Client{personal, work},
		presendFromI: 1, // sending as Work
	}

	if got := m.sentDraftsIMAPClient(); got != personal {
		t.Fatal("sentDraftsIMAPClient() should default to the primary IMAP account")
	}
}

func TestSentDraftsIMAPClient_FollowsSendingAccountWhenEnabled(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.AccountConfig{
			{Name: "Personal", From: "me@example.com"},
			{Name: "Work", From: "me@work.example"},
		},
		StoreSentDraftsInSendingAccount: true,
	}
	personal := imap.New(imap.Config{Host: "personal"})
	work := imap.New(imap.Config{Host: "work"})
	m := Model{
		cfg:          cfg,
		accounts:     cfg.ActiveAccounts(),
		clients:      []*imap.Client{personal, work},
		presendFromI: 1, // sending as Work
	}

	if got := m.sentDraftsIMAPClient(); got != work {
		t.Fatal("sentDraftsIMAPClient() should follow the selected sending account when enabled")
	}
}

func TestMatchFromAddress(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.AccountConfig{
			{Name: "Personal", From: "Me <me@example.com>"},
		},
		Senders: []config.SenderConfig{
			{Name: "Work", From: "Me <me@work.example>"},
		},
	}
	m := Model{cfg: cfg, accounts: cfg.ActiveAccounts()}
	if got := m.matchFromAddress("me@work.example"); got != 1 {
		t.Fatalf("matchFromAddress() = %d, want 1", got)
	}
}

func TestActiveFolderUsesOffTabFolder(t *testing.T) {
	m := Model{
		cfg: &config.Config{
			Folders: config.FoldersConfig{
				Inbox:  "INBOX",
				Drafts: "Drafts",
				Spam:   "Spam",
			},
		},
		folders:       []string{"Inbox"},
		activeFolderI: 0,
	}

	m.offTabFolder = "Drafts"
	if got := m.activeFolder(); got != "Drafts" {
		t.Fatalf("activeFolder() with Drafts off-tab = %q, want %q", got, "Drafts")
	}

	m.offTabFolder = "Spam"
	if got := m.activeFolder(); got != "Spam" {
		t.Fatalf("activeFolder() with Spam off-tab = %q, want %q", got, "Spam")
	}
}

func TestUpdateInboxEscClearsCommittedFilter(t *testing.T) {
	m := Model{
		filterText: "invoice",
		inbox:      newInboxList(80, 20, "", ""),
		folders:    []string{"Inbox"},
		cfg: &config.Config{
			Folders: config.FoldersConfig{Inbox: "INBOX"},
		},
	}

	next, _ := m.updateInbox(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(Model)
	if got.filterText != "" {
		t.Fatalf("filterText = %q, want empty", got.filterText)
	}
	if got.filterActive {
		t.Fatal("filterActive should be false after esc")
	}
}

func TestValidateScreenerSafetyRejectsTrashDestination(t *testing.T) {
	m := Model{
		cfg: &config.Config{
			Folders: config.FoldersConfig{
				Trash:       "Trash",
				ScreenedOut: "Trash",
			},
		},
	}

	err := m.validateScreenerSafety()
	if err == nil {
		t.Fatal("expected validateScreenerSafety to fail when ScreenedOut points to Trash")
	}
}

func TestUpdateComposeEscRequestsDiscardConfirmation(t *testing.T) {
	m := Model{
		compose: newComposeModel(),
	}
	m.compose.to.SetValue("alice@example.com")
	m.state = stateCompose

	next, _ := m.updateCompose(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(Model)
	if !got.pendingDiscard {
		t.Fatal("expected pendingDiscard after esc with unsent compose data")
	}
	if got.state != stateCompose {
		t.Fatalf("state = %v, want compose", got.state)
	}
	if got.status == "" {
		t.Fatal("expected discard confirmation status")
	}
}

func TestUpdateComposeDiscardConfirmationYClearsState(t *testing.T) {
	m := Model{
		compose:        newComposeModel(),
		attachments:    []string{"/tmp/file.txt"},
		pendingDiscard: true,
		state:          stateCompose,
	}
	m.compose.to.SetValue("alice@example.com")

	next, _ := m.updateCompose(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got := next.(Model)
	if got.pendingDiscard {
		t.Fatal("pendingDiscard should be cleared after confirming discard")
	}
	if got.state != stateInbox {
		t.Fatalf("state = %v, want inbox", got.state)
	}
	if len(got.attachments) != 0 {
		t.Fatalf("attachments = %#v, want cleared", got.attachments)
	}
}

func TestUpdatePresendEscRequestsDiscardConfirmation(t *testing.T) {
	m := Model{
		pendingSend: &pendingSendData{
			to:      "alice@example.com",
			subject: "hello",
			body:    "body",
		},
		state: statePresend,
	}

	next, _ := m.updatePresend(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(Model)
	if !got.pendingDiscard {
		t.Fatal("expected pendingDiscard after esc in pre-send")
	}
	if got.state != statePresend {
		t.Fatalf("state = %v, want pre-send", got.state)
	}
}

func TestMarkAsReadTimer(t *testing.T) {
	t.Run("config determines marking behavior", func(t *testing.T) {
		tests := []struct {
			name      string
			configSec int
			wantTimer bool
		}{
			{"immediate when 0", 0, false},
			{"timer when > 0", 7, true},
			{"timer when custom", 15, true},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cfg := &config.Config{
					UI: config.UIConfig{
						MarkAsReadAfterSecs: tt.configSec,
					},
				}

				if (cfg.UI.MarkAsReadAfterSecs > 0) != tt.wantTimer {
					t.Errorf("MarkAsReadAfterSecs=%d should trigger timer=%v", tt.configSec, tt.wantTimer)
				}
			})
		}
	})

	t.Run("timer state management", func(t *testing.T) {
		m := Model{
			cfg: &config.Config{
				UI: config.UIConfig{
					MarkAsReadAfterSecs: 7,
				},
			},
		}

		// Initially empty
		if m.markAsReadUID != 0 || m.markAsReadFolder != "" {
			t.Errorf("initial timer state should be empty")
		}

		// Set timer state (simulates bodyLoadedMsg behavior)
		m.markAsReadUID = 123
		m.markAsReadFolder = "INBOX"

		if m.markAsReadUID != 123 || m.markAsReadFolder != "INBOX" {
			t.Errorf("timer state not set correctly: uid=%d folder=%q", m.markAsReadUID, m.markAsReadFolder)
		}

		// Clear timer state (simulates exit reader or timer completion)
		m.markAsReadUID = 0
		m.markAsReadFolder = ""

		if m.markAsReadUID != 0 || m.markAsReadFolder != "" {
			t.Errorf("timer state not cleared: uid=%d folder=%q", m.markAsReadUID, m.markAsReadFolder)
		}
	})

	t.Run("timer ignored when user exits reader early", func(t *testing.T) {
		m := Model{
			cfg: &config.Config{
				UI: config.UIConfig{
					MarkAsReadAfterSecs: 7,
				},
			},
			state: stateInbox, // user exited reader
			emails: []imap.Email{
				{UID: 123, Folder: "INBOX", Seen: false},
			},
			markAsReadUID:    0, // cleared when exiting reader
			markAsReadFolder: "",
		}

		// Timer fires but user already left reader
		msg := markAsReadTimerMsg{uid: 123, folder: "INBOX"}
		_, _ = m.Update(msg)

		// Email should still be unread
		if m.emails[0].Seen {
			t.Errorf("email marked as seen even though user exited reader")
		}
	})

	t.Run("timer state cleared when exiting reader", func(t *testing.T) {
		m := Model{
			cfg: &config.Config{
				UI: config.UIConfig{
					MarkAsReadAfterSecs: 7,
				},
			},
			state:            stateReading,
			markAsReadUID:    123,
			markAsReadFolder: "INBOX",
		}

		// User presses 'q' to exit reader
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}
		updated, _ := m.updateReader(msg)
		m = updated.(Model)

		// Timer state should be cleared
		if m.markAsReadUID != 0 || m.markAsReadFolder != "" {
			t.Errorf("timer state not cleared when exiting reader")
		}

		// State should be back to inbox
		if m.state != stateInbox {
			t.Errorf("state not returned to inbox")
		}
	})
}

func TestHandleEverythingResultKeepsRealSubject(t *testing.T) {
	m := Model{
		inbox: newInboxList(80, 20, "", ""),
	}
	msg := everythingResultMsg{
		emails: []imap.Email{{UID: 1, Folder: "Sent", Subject: "Quarterly update"}},
	}

	next, _ := m.handleEverythingResult(msg)
	got := next.(*Model)
	if got.emails[0].Subject != "Quarterly update" {
		t.Fatalf("subject = %q, want unchanged real subject", got.emails[0].Subject)
	}
}

func TestReplyAllExcludesAllOwnAddresses(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.Config
		email    *imap.Email
		wantCC   string
		wantExcl []string // addresses that should be excluded
	}{
		{
			name: "single account - exclude From",
			cfg: &config.Config{
				Accounts: []config.AccountConfig{
					{User: "simon@ssp.sh", From: "Simon Späti <simon@ssp.sh>"},
				},
			},
			email: &imap.Email{
				From: "kristen@rilldata.com",
				To:   "simon@ssp.sh",
				CC:   "marianne@rilldata.com",
			},
			wantCC:   "marianne@rilldata.com",
			wantExcl: []string{"simon@ssp.sh"},
		},
		{
			name: "user != from (critical edge case)",
			cfg: &config.Config{
				Accounts: []config.AccountConfig{
					{User: "user123@mail.provider.com", From: "Simon Späti <simon@ssp.sh>"},
				},
			},
			email: &imap.Email{
				From: "alice@example.com",
				To:   "user123@mail.provider.com",
				CC:   "bob@example.com",
			},
			wantCC:   "bob@example.com",
			wantExcl: []string{"user123@mail.provider.com", "simon@ssp.sh"},
		},
		{
			name: "multiple accounts - exclude all",
			cfg: &config.Config{
				Accounts: []config.AccountConfig{
					{User: "personal@example.com", From: "Me <personal@example.com>"},
					{User: "work@company.com", From: "Me <work@company.com>"},
				},
			},
			email: &imap.Email{
				From: "client@business.com",
				To:   "work@company.com, client-team@business.com",
				CC:   "personal@example.com, other@business.com",
			},
			wantCC:   "client-team@business.com, other@business.com",
			wantExcl: []string{"work@company.com", "personal@example.com"},
		},
		{
			name: "sender aliases excluded",
			cfg: &config.Config{
				Accounts: []config.AccountConfig{
					{User: "me@example.com", From: "Me <me@example.com>"},
				},
				Senders: []config.SenderConfig{
					{From: "Support <support@example.com>"},
				},
			},
			email: &imap.Email{
				From: "customer@client.com",
				To:   "support@example.com",
				CC:   "me@example.com, customer-team@client.com",
			},
			wantCC:   "customer-team@client.com",
			wantExcl: []string{"me@example.com", "support@example.com"},
		},
		{
			name: "case insensitive matching",
			cfg: &config.Config{
				Accounts: []config.AccountConfig{
					{User: "simon@ssp.sh", From: "Simon <simon@ssp.sh>"},
				},
			},
			email: &imap.Email{
				From: "alice@example.com",
				To:   "SIMON@SSP.SH",
				CC:   "Simon <Simon@Ssp.Sh>, bob@example.com",
			},
			wantCC:   "bob@example.com",
			wantExcl: []string{"simon@ssp.sh"},
		},
		{
			name: "named addresses with brackets",
			cfg: &config.Config{
				Accounts: []config.AccountConfig{
					{User: "me@work.com", From: "John Doe <me@work.com>"},
				},
			},
			email: &imap.Email{
				From: "Jane <jane@client.com>",
				To:   "John Doe <me@work.com>",
				CC:   "Alice <alice@client.com>, Bob <me@work.com>",
			},
			wantCC:   "Alice <alice@client.com>",
			wantExcl: []string{"me@work.com"},
		},
		{
			name: "empty CC when all recipients are self",
			cfg: &config.Config{
				Accounts: []config.AccountConfig{
					{User: "me@example.com", From: "Me <me@example.com>"},
				},
			},
			email: &imap.Email{
				From: "sender@client.com",
				To:   "me@example.com",
				CC:   "",
			},
			wantCC:   "",
			wantExcl: []string{"me@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{
				cfg:      tt.cfg,
				accounts: tt.cfg.ActiveAccounts(),
			}

			// Build the exclusion set exactly as launchReplyWithCC does
			ownAddrs := make(map[string]bool)
			for _, acc := range m.accounts {
				ownAddrs[strings.ToLower(extractEmailAddr(acc.User))] = true
			}
			for _, from := range m.presendFroms() {
				ownAddrs[strings.ToLower(extractEmailAddr(from))] = true
			}

			// Verify all expected addresses are in the exclusion set
			for _, excl := range tt.wantExcl {
				lowerExcl := strings.ToLower(extractEmailAddr(excl))
				if !ownAddrs[lowerExcl] {
					t.Errorf("expected %q to be in exclusion set, but it's missing", excl)
				}
			}

			// Simulate the reply-all CC building logic
			var parts []string
			for _, addr := range splitAddrs(tt.email.To + "," + tt.email.CC) {
				if a := strings.TrimSpace(addr); a != "" {
					addrLower := strings.ToLower(extractEmailAddr(a))
					if !ownAddrs[addrLower] {
						parts = append(parts, a)
					}
				}
			}
			gotCC := strings.Join(parts, ", ")

			if gotCC != tt.wantCC {
				t.Errorf("reply-all CC = %q, want %q", gotCC, tt.wantCC)
			}

			// Double-check: verify none of the excluded addresses appear in the result
			for _, excl := range tt.wantExcl {
				if strings.Contains(strings.ToLower(gotCC), strings.ToLower(extractEmailAddr(excl))) {
					t.Errorf("excluded address %q should not appear in CC: %q", excl, gotCC)
				}
			}
		})
	}
}

func TestIsMimeMismatch(t *testing.T) {
	tests := []struct {
		name     string
		ext      string
		detected string
		want     bool
	}{
		// Disguised files — should be flagged
		{"sh disguised as png", ".png", "text/plain; charset=utf-8", true},
		{"html disguised as jpg", ".jpg", "text/html; charset=utf-8", true},
		{"elf binary as pdf", ".pdf", "application/octet-stream", true},
		{"script as gif", ".gif", "text/plain; charset=utf-8", true},

		// Legitimate files — should pass
		{"real png", ".png", "image/png", false},
		{"real jpg", ".jpg", "image/jpeg", false},
		{"real gif", ".gif", "image/gif", false},
		{"real pdf", ".pdf", "application/pdf", false},
		{"real zip", ".zip", "application/zip", false},
		{"real mp3", ".mp3", "audio/mpeg", false},
		{"real mp4", ".mp4", "video/mp4", false},

		// Unknown extensions — can't validate, should pass through
		{"unknown ext .xyz", ".xyz", "text/plain; charset=utf-8", false},
		{"unknown ext .foo", ".foo", "application/octet-stream", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMimeMismatch(tt.ext, tt.detected)
			if got != tt.want {
				t.Errorf("isMimeMismatch(%q, %q) = %v, want %v", tt.ext, tt.detected, got, tt.want)
			}
		})
	}
}

func TestDangerousExts(t *testing.T) {
	// Verify known dangerous extensions are in the blocklist.
	dangerous := []string{".sh", ".exe", ".desktop", ".bat", ".py", ".jar", ".ps1"}
	for _, ext := range dangerous {
		if !dangerousExts[ext] {
			t.Errorf("expected %q in dangerousExts", ext)
		}
	}
	// Verify safe extensions are NOT in the blocklist.
	safe := []string{".png", ".jpg", ".pdf", ".txt", ".html", ".md"}
	for _, ext := range safe {
		if dangerousExts[ext] {
			t.Errorf("unexpected %q in dangerousExts", ext)
		}
	}
}

func TestMimeMismatchWithRealBytes(t *testing.T) {
	// Simulate real magic-byte detection scenarios using net/http.DetectContentType.
	tests := []struct {
		name    string
		ext     string
		data    []byte // fake file content
		wantBad bool
	}{
		{
			name:    "bash script disguised as .png",
			ext:     ".png",
			data:    []byte("#!/bin/bash\necho hello\n"),
			wantBad: true,
		},
		{
			name:    "python script disguised as .jpg",
			ext:     ".jpg",
			data:    []byte("#!/usr/bin/env python3\nprint('hello')\n"),
			wantBad: true,
		},
		{
			name:    "html with script disguised as .pdf",
			ext:     ".pdf",
			data:    []byte("<html><body>harmless content</body></html>"),
			wantBad: true,
		},
		{
			name: "real PNG file (magic bytes)",
			ext:  ".png",
			// PNG magic: 89 50 4E 47 0D 0A 1A 0A
			data:    []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00},
			wantBad: false,
		},
		{
			name: "real PDF file (magic bytes)",
			ext:  ".pdf",
			// PDF magic: %PDF-
			data:    []byte("%PDF-1.4 fake pdf content here"),
			wantBad: false,
		},
		{
			name: "real GIF file (magic bytes)",
			ext:  ".gif",
			// GIF magic: GIF89a
			data:    []byte("GIF89a" + strings.Repeat("\x00", 100)),
			wantBad: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected := http.DetectContentType(tt.data)
			got := isMimeMismatch(tt.ext, detected)
			if got != tt.wantBad {
				t.Errorf("isMimeMismatch(%q, %q) = %v, want %v (data: %q...)",
					tt.ext, detected, got, tt.wantBad, string(tt.data[:min(20, len(tt.data))]))
			}
		})
	}
}
