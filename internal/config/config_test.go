package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandEnv(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		envKey string
		envVal string
		want   string
	}{
		{"bare $VAR", "$MY_PASS", "MY_PASS", "secret", "secret"},
		{"braced ${VAR}", "${MY_PASS}", "MY_PASS", "secret", "secret"},
		{"embedded dollar", "literal$value", "", "", "literal$value"},
		{"multiple dollars", "pa$$word", "", "", "pa$$word"},
		{"empty string", "", "", "", ""},
		{"unset var bare", "$UNSET_NEOMD_VAR", "", "", ""},
		{"unset var braced", "${UNSET_NEOMD_VAR}", "", "", ""},
		{"bare $ alone", "$", "", "", ""},
		{"empty braced ${}", "${}", "", "", ""},
		{"whitespace trimmed", "  $MY_VAR  ", "MY_VAR", "trimmed", "trimmed"},
		{"$VAR with suffix", "$MY_VAR-suffix", "", "", ""},
		{"text before $VAR", "prefix-$MY_VAR", "", "", "prefix-$MY_VAR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envKey != "" {
				t.Setenv(tt.envKey, tt.envVal)
			}
			got := expandEnv(tt.input)
			if got != tt.want {
				t.Errorf("expandEnv(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("cannot determine home dir: %v", err)
	}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"tilde prefix", "~/mail", filepath.Join(home, "mail")},
		{"bare tilde", "~", home},
		{"absolute path", "/absolute/path", "/absolute/path"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandPath(tt.input)
			if got != tt.want {
				t.Errorf("expandPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTabLabels(t *testing.T) {
	tests := []struct {
		name     string
		tabOrder []string
		wantFirst []string // check these labels appear at the start
	}{
		{
			"empty tab_order returns defaults",
			nil,
			[]string{"Inbox", "ToScreen", "Feed"},
		},
		{
			"custom order",
			[]string{"inbox", "feed"},
			[]string{"Inbox", "Feed"},
		},
		{
			"unknown key skipped",
			[]string{"inbox", "nonexistent", "feed"},
			[]string{"Inbox", "Feed"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := FoldersConfig{TabOrder: tt.tabOrder}
			got := f.TabLabels()
			if len(got) < len(tt.wantFirst) {
				t.Fatalf("got %d labels, want at least %d", len(got), len(tt.wantFirst))
			}
			for i, want := range tt.wantFirst {
				if got[i] != want {
					t.Errorf("TabLabels()[%d] = %q, want %q", i, got[i], want)
				}
			}
			// For the custom order cases the length should match exactly.
			if tt.tabOrder != nil {
				wantLen := 0
				for _, k := range tt.tabOrder {
					if _, ok := keyToLabel[k]; ok {
						wantLen++
					}
				}
				if len(got) != wantLen {
					t.Errorf("TabLabels() returned %d labels, want %d", len(got), wantLen)
				}
			}
		})
	}
}

func TestAutoScreen(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	tests := []struct {
		name string
		val  *bool
		want bool
	}{
		{"nil defaults to true", nil, true},
		{"explicit false", boolPtr(false), false},
		{"explicit true", boolPtr(true), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := UIConfig{AutoScreenOnLoad: tt.val}
			if got := u.AutoScreen(); got != tt.want {
				t.Errorf("AutoScreen() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDraftBackups(t *testing.T) {
	tests := []struct {
		name  string
		count int
		want  int
	}{
		{"zero defaults to 20", 0, 20},
		{"explicit value", 5, 5},
		{"negative disables", -1, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := UIConfig{DraftBackupCount: tt.count}
			if got := u.DraftBackups(); got != tt.want {
				t.Errorf("DraftBackups() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBulkThreshold(t *testing.T) {
	tests := []struct {
		name string
		val  int
		want int
	}{
		{"zero defaults to 10", 0, 10},
		{"explicit value", 5, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := UIConfig{BulkProgressThreshold: tt.val}
			if got := u.BulkThreshold(); got != tt.want {
				t.Errorf("BulkThreshold() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLabelFor(t *testing.T) {
	// Custom IMAP folder names (e.g. HEY-style labels).
	fc := FoldersConfig{
		Inbox:      "INBOX",
		PaperTrail: "HEY/Paper Trail",
		Feed:       "Newsletters",
		Sent:       "Sent Items",
	}
	tests := []struct {
		imap, want string
	}{
		{"INBOX", "Inbox"},
		{"HEY/Paper Trail", "PaperTrail"},
		{"Newsletters", "Feed"},
		{"Sent Items", "Sent"},
		{"unknown-folder", "unknown-folder"}, // pass-through fallback
	}
	for _, tt := range tests {
		if got := fc.LabelFor(tt.imap); got != tt.want {
			t.Errorf("LabelFor(%q) = %q, want %q", tt.imap, got, tt.want)
		}
	}
}

func TestFolderAllowed_MatchesLabelAfterCustomIMAPName(t *testing.T) {
	// Regression: when notifications.folders = ["PaperTrail"] but
	// folders.papertrail = "HEY/Paper Trail", the caller must convert the
	// IMAP name to the label before calling FolderAllowed.
	fc := FoldersConfig{PaperTrail: "HEY/Paper Trail"}
	nc := NotificationsConfig{Folders: []string{"PaperTrail"}}

	if nc.FolderAllowed("HEY/Paper Trail") {
		t.Error("FolderAllowed should not match an IMAP name directly — caller must normalise to label first")
	}
	if !nc.FolderAllowed(fc.LabelFor("HEY/Paper Trail")) {
		t.Error("FolderAllowed should match after caller normalises via LabelFor")
	}
}

func TestLoad_MissingConfigCreatesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neomd", "config.toml")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error from Load with missing config")
	}
	if !strings.Contains(err.Error(), "please fill in") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "please fill in")
	}
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		t.Errorf("expected default config to be created at %s", path)
	}
}

func TestWriteDefault_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := defaults()
	if err := writeDefault(path, cfg); err != nil {
		t.Fatalf("writeDefault() error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat error: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0600 {
		t.Errorf("file mode = %04o, want 0600", mode)
	}
}

func TestExpandEnv_PasswordSafety(t *testing.T) {
	// Passwords containing dollar signs must never be mangled.
	dangerous := []string{"pa$$word", "s3cr3t$", "$not$a$var"}
	for _, pw := range dangerous {
		got := expandEnv(pw)
		if got != pw {
			t.Errorf("expandEnv(%q) = %q — password was mangled!", pw, got)
		}
	}
}

func TestLoad_ErrorMessageNoPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Write syntactically invalid TOML that also contains a password-like string.
	password := "SuperSecret123!"
	content := `[account]
user = "test@example.com"
password = "` + password + `"
invalid toml here !!!
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error from Load with invalid TOML")
	}
	if strings.Contains(err.Error(), password) {
		t.Errorf("error message contains password %q — potential leak", password)
	}
}

func TestValidateHostPort(t *testing.T) {
	tests := []struct {
		addr    string
		wantErr bool
	}{
		{"imap.example.com:993", false},
		{"localhost:143", false},
		{"127.0.0.1:1143", false},
		{"mail.example.com:65535", false},
		// Invalid
		{"imap.example.com", true},    // no port
		{":993", true},                // no host
		{"imap.example.com:0", true},  // port 0
		{"imap.example.com:99999", true}, // port > 65535
		{"imap.example.com:abc", true},   // non-numeric port
		{"", true},                       // empty
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			err := validateHostPort(tt.addr, "test", "imap")
			if (err != nil) != tt.wantErr {
				t.Errorf("validateHostPort(%q) error = %v, wantErr %v", tt.addr, err, tt.wantErr)
			}
		})
	}
}

func TestValidate_MissingAccount(t *testing.T) {
	cfg := &Config{}
	err := cfg.validate()
	if err == nil {
		t.Error("expected error for config with no accounts")
	}
}

func TestValidate_InvalidPort(t *testing.T) {
	cfg := &Config{
		Accounts: []AccountConfig{{
			Name: "Test",
			IMAP: "imap.example.com:99999",
			SMTP: "smtp.example.com:587",
			User: "test@example.com",
		}},
	}
	err := cfg.validate()
	if err == nil {
		t.Error("expected error for invalid IMAP port")
	}
	if !strings.Contains(err.Error(), "port") {
		t.Errorf("error should mention port, got: %v", err)
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := &Config{
		Accounts: []AccountConfig{{
			Name: "Test",
			IMAP: "imap.example.com:993",
			SMTP: "smtp.example.com:587",
			User: "test@example.com",
		}},
	}
	err := cfg.validate()
	if err != nil {
		t.Errorf("valid config should not error, got: %v", err)
	}
}

func TestValidate_NegativeUIValues(t *testing.T) {
	cfg := &Config{
		Accounts: []AccountConfig{{
			Name: "Test",
			IMAP: "imap.example.com:993",
			SMTP: "smtp.example.com:587",
			User: "test@example.com",
		}},
		UI: UIConfig{InboxCount: -1},
	}
	err := cfg.validate()
	if err == nil {
		t.Error("expected error for negative inbox_count")
	}
}
