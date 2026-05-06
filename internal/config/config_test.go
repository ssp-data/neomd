package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	zalandokeyring "github.com/zalando/go-keyring"
	"github.com/sspaeti/neomd/internal/keyring"
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

func TestLoad_AIConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfgBody := `
[[accounts]]
name     = "Personal"
imap     = "imap.example.com:993"
smtp     = "smtp.example.com:587"
user     = "me@example.com"
password = "x"
from     = "Me <me@example.com>"

[ai]
command = "claude"
args = ["--print", "--cwd"]
`
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AI.Command != "claude" {
		t.Errorf("AI.Command = %q, want %q", cfg.AI.Command, "claude")
	}
	if len(cfg.AI.Args) != 2 || cfg.AI.Args[0] != "--print" || cfg.AI.Args[1] != "--cwd" {
		t.Errorf("AI.Args = %v, want [--print --cwd]", cfg.AI.Args)
	}
}

func TestLoad_AIConfigOmittedFallsBackToDefault(t *testing.T) {
	// When [ai] is absent, defaults() supplies "claude" so the i key works
	// out of the box. Users who want the binding disabled set command = "".
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfgBody := `
[[accounts]]
name     = "Personal"
imap     = "imap.example.com:993"
smtp     = "smtp.example.com:587"
user     = "me@example.com"
password = "x"
from     = "Me <me@example.com>"
`
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AI.Command != "claude" {
		t.Errorf("AI.Command = %q, want %q (default from defaults())", cfg.AI.Command, "claude")
	}
}

func TestLoad_AIConfigEmptyStringDisables(t *testing.T) {
	// Setting command = "" must remain empty (override default) so the
	// binding can be disabled deliberately.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfgBody := `
[[accounts]]
name     = "Personal"
imap     = "imap.example.com:993"
smtp     = "smtp.example.com:587"
user     = "me@example.com"
password = "x"
from     = "Me <me@example.com>"

[ai]
command = ""
`
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AI.Command != "" {
		t.Errorf("AI.Command = %q, want empty (explicit disable)", cfg.AI.Command)
	}
}

func TestUseKeyring(t *testing.T) {
	tests := []struct {
		password string
		want     bool
	}{
		{"keyring", true},
		{"actual-password", false},
		{"", false},
		{"$ENV_VAR", false},
		{"keyring-like", false},
	}
	for _, tt := range tests {
		acc := AccountConfig{Password: tt.password}
		if got := acc.UseKeyring(); got != tt.want {
			t.Errorf("UseKeyring(%q) = %v, want %v", tt.password, got, tt.want)
		}
	}
}

func TestResolveKeyringPassword(t *testing.T) {
	// Use the in-memory mock provided by zalando/go-keyring so tests don't
	// touch the real OS keyring.
	zalandokeyring.MockInit()

	t.Run("resolved when entry exists", func(t *testing.T) {
		const acct = "TestAcctResolved"
		if err := keyring.SetPassword(acct, "real-secret"); err != nil {
			t.Fatalf("SetPassword: %v", err)
		}
		got := resolveKeyringPassword(acct, "keyring")
		if got != "real-secret" {
			t.Errorf("got %q, want resolved password", got)
		}
		_ = keyring.DeletePassword(acct)
	})

	t.Run("sentinel preserved when entry missing", func(t *testing.T) {
		got := resolveKeyringPassword("MissingAcct", "keyring")
		if got != "keyring" {
			t.Errorf("got %q, want sentinel preserved", got)
		}
	})

	t.Run("non-sentinel passthrough", func(t *testing.T) {
		got := resolveKeyringPassword("any", "literal-password")
		if got != "literal-password" {
			t.Errorf("got %q, want passthrough", got)
		}
	})

	t.Run("empty account name passthrough", func(t *testing.T) {
		// Anonymous accounts (empty Name) should not trigger a keyring lookup.
		got := resolveKeyringPassword("", "keyring")
		if got != "keyring" {
			t.Errorf("got %q, want passthrough for empty account", got)
		}
	})
}

// TestLoad_KeyringResolvesAcrossSenders verifies the senders-gap fix:
// PR #5 resolved keyring at IMAP construction time, leaving SMTP send paths
// reading the literal "keyring" sentinel. By moving resolution into Load(),
// every consumer (IMAP, SMTP, [[senders]] aliases) sees the resolved value.
func TestLoad_KeyringResolvesAcrossSenders(t *testing.T) {
	zalandokeyring.MockInit()
	const acctName = "Personal"
	const realPw = "the-real-password"
	if err := keyring.SetPassword(acctName, realPw); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	defer keyring.DeletePassword(acctName)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfgBody := `
[[accounts]]
name     = "Personal"
imap     = "imap.example.com:993"
smtp     = "smtp.example.com:587"
user     = "me@example.com"
password = "keyring"
from     = "Me <me@example.com>"

[[senders]]
name    = "Alias"
from    = "Alias <alias@example.com>"
account = "Personal"
`
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(cfg.Accounts))
	}
	if got := cfg.Accounts[0].Password; got != realPw {
		t.Errorf("Accounts[0].Password = %q, want resolved %q (the senders-gap fix)", got, realPw)
	}
	if cfg.Accounts[0].UseKeyring() {
		t.Error("UseKeyring() should be false after successful resolution")
	}
}

func TestLoad_AIConfigDefaultUsesInteractiveClaude(t *testing.T) {
	// Regression for two prior bugs:
	//   1. Defaults must NOT include `-p` (claude's print mode bills against
	//      API credits even with a Pro/Max subscription — triggered "Credit
	//      balance is too low" in real use).
	//   2. The arg string must mention {file} so claude knows which file to
	//      edit. Without it the spawn was `claude "fix grammar" /path/file`
	//      — claude treated the path positional as ignored text and started
	//      running `git status` in its cwd instead of editing the draft.
	//
	// Default `args = ["edit {file}: {prompt}"]` paired with cmd.Dir set to
	// the temp dir lets claude reach the file via its built-in Edit tool.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfgBody := `
[[accounts]]
name     = "Personal"
imap     = "imap.example.com:993"
smtp     = "smtp.example.com:587"
user     = "me@example.com"
password = "x"
from     = "Me <me@example.com>"
`
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.AI.Args) != 1 {
		t.Fatalf("AI.Args = %v, want exactly one arg", cfg.AI.Args)
	}
	got := cfg.AI.Args[0]
	if !strings.Contains(got, "{file}") {
		t.Errorf("AI.Args = %q — must contain {file} placeholder so claude can locate the draft", got)
	}
	if !strings.Contains(got, "{prompt}") {
		t.Errorf("AI.Args = %q — must contain {prompt} placeholder so the typed instruction is forwarded", got)
	}
	for _, a := range cfg.AI.Args {
		if a == "-p" || a == "--print" {
			t.Errorf("default args contain %q — that forces API billing instead of subscription auth", a)
		}
	}
}
