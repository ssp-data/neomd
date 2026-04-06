// Package config loads neomd configuration from ~/.config/neomd/config.toml.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/sspaeti/neomd/internal/keyring"
)

// SenderConfig holds a named "From" alias used only for sending.
// Unlike AccountConfig it requires no IMAP connection — only the From header
// changes. Set Account to the name of an [[accounts]] entry whose SMTP
// credentials to use; leave empty to use the active account's SMTP.
type SenderConfig struct {
	Name    string `toml:"name"`
	From    string `toml:"from"`    // "Display Name <email@example.com>"
	Account string `toml:"account"` // optional: account name whose SMTP to use
}

// AccountConfig holds IMAP/SMTP connection settings.
type AccountConfig struct {
	Name     string `toml:"name"`
	IMAP     string `toml:"imap"` // host:port (993 = TLS, 143 = STARTTLS)
	SMTP     string `toml:"smtp"` // host:port (587 = STARTTLS, 465 = TLS)
	User     string `toml:"user"`
	Password string `toml:"password"`
	From     string `toml:"from"` // "Name <email@example.com>"
	STARTTLS bool   `toml:"starttls"`

	// OAuth2 fields — only used when auth_type = "oauth2".
	AuthType           string   `toml:"auth_type"` // "plain" (default) | "oauth2"
	OAuth2ClientID     string   `toml:"oauth2_client_id"`
	OAuth2ClientSecret string   `toml:"oauth2_client_secret"`
	OAuth2IssuerURL    string   `toml:"oauth2_issuer_url"` // OIDC discovery endpoint (e.g. "https://accounts.google.com")
	OAuth2AuthURL      string   `toml:"oauth2_auth_url"`   // manual override; skips discovery
	OAuth2TokenURL     string   `toml:"oauth2_token_url"`  // manual override; skips discovery
	OAuth2Scopes       []string `toml:"oauth2_scopes"`
	OAuth2RedirectPort int      `toml:"oauth2_redirect_port"` // local callback port; default 8085
}

// IsOAuth2 reports whether this account uses OAuth2 instead of password auth.
func (a AccountConfig) IsOAuth2() bool {
	return strings.EqualFold(a.AuthType, "oauth2")
}

// UseKeyring reports whether this account uses the OS keyring for password storage.
// Returns true if password is set to the sentinel value "keyring".
func (a AccountConfig) UseKeyring() bool {
	return strings.EqualFold(a.Password, "keyring")
}

// ResolvedPassword returns the actual password for this account.
// If UseKeyring() is true, it fetches from the OS keyring.
// Otherwise, returns the password field directly.
// Returns empty string and error if keyring lookup fails.
func (a AccountConfig) ResolvedPassword() (string, error) {
	if a.UseKeyring() {
		return keyring.GetPassword(a.Name)
	}
	return a.Password, nil
}

// ScreenerConfig points to the allowlist/blocklist files.
type ScreenerConfig struct {
	ScreenedIn  string `toml:"screened_in"`
	ScreenedOut string `toml:"screened_out"`
	Feed        string `toml:"feed"`
	PaperTrail  string `toml:"papertrail"`
	Spam        string `toml:"spam"`
}

// FoldersConfig maps logical names to actual IMAP mailbox names.
type FoldersConfig struct {
	Inbox       string `toml:"inbox"`
	Sent        string `toml:"sent"`
	Trash       string `toml:"trash"`
	Drafts      string `toml:"drafts"`
	ToScreen    string `toml:"to_screen"`
	Feed        string `toml:"feed"`
	PaperTrail  string `toml:"papertrail"`
	ScreenedOut string `toml:"screened_out"`
	Archive     string `toml:"archive"`
	Waiting     string `toml:"waiting"`
	Scheduled   string `toml:"scheduled"`
	Someday     string `toml:"someday"`
	Spam        string `toml:"spam"`
	// TabOrder lists folder keys (e.g. "inbox", "to_screen") in the desired
	// tab display order. Spam is always excluded from tabs regardless of order.
	// If empty, the built-in default order is used.
	TabOrder []string `toml:"tab_order"`
}

// defaultTabOrder is the built-in tab order when tab_order is not configured.
var defaultTabOrder = []string{"inbox", "to_screen", "feed", "papertrail", "waiting", "someday", "scheduled", "sent", "archive", "screened_out", "drafts", "trash"}

// keyToLabel maps config key names to the internal label names used by the UI.
// These labels are what m.folders stores and what activeFolder() matches against.
var keyToLabel = map[string]string{
	"inbox":        "Inbox",
	"sent":         "Sent",
	"trash":        "Trash",
	"drafts":       "Drafts",
	"to_screen":    "ToScreen",
	"feed":         "Feed",
	"papertrail":   "PaperTrail",
	"screened_out": "ScreenedOut",
	"archive":      "Archive",
	"waiting":      "Waiting",
	"scheduled":    "Scheduled",
	"someday":      "Someday",
}

// TabLabels returns the UI label names in tab display order.
// tab_order keys (e.g. "inbox", "to_screen") are resolved to label names
// (e.g. "Inbox", "ToScreen") that activeFolder() and keyboard shortcuts match against.
// Spam is excluded — it is never shown as a tab.
func (f FoldersConfig) TabLabels() []string {
	keys := f.TabOrder
	if len(keys) == 0 {
		keys = defaultTabOrder
	}
	tabs := make([]string, 0, len(keys))
	for _, k := range keys {
		if label, ok := keyToLabel[k]; ok {
			tabs = append(tabs, label)
		}
	}
	return tabs
}

// UIConfig holds display preferences.
type UIConfig struct {
	Theme                 string `toml:"theme"`                   // dark | light | auto
	InboxCount            int    `toml:"inbox_count"`             // number of messages to fetch
	Signature             string `toml:"signature"`               // appended to new compose buffers (markdown)
	AutoScreenOnLoad      *bool  `toml:"auto_screen_on_load"`     // screen inbox on every load (default true)
	BgSyncInterval        int    `toml:"bg_sync_interval"`        // background sync interval in minutes (0 = disabled, default 5)
	BulkProgressThreshold int    `toml:"bulk_progress_threshold"` // show progress counter for batches larger than this (default 10)
}

// BulkThreshold returns the configured bulk progress threshold (default 10).
func (u UIConfig) BulkThreshold() int {
	if u.BulkProgressThreshold <= 0 {
		return 10
	}
	return u.BulkProgressThreshold
}

// AutoScreen returns true if auto-screen-on-inbox-load is enabled (default: true).
func (u UIConfig) AutoScreen() bool {
	if u.AutoScreenOnLoad == nil {
		return true
	}
	return *u.AutoScreenOnLoad
}

// Config is the root neomd configuration.
type Config struct {
	// Accounts is the list of email accounts (use [[accounts]] in config.toml).
	// For a single account the legacy [account] block is also accepted.
	Accounts []AccountConfig `toml:"accounts"`
	Account  AccountConfig   `toml:"account"` // legacy single-account fallback

	// Senders is a list of extra "From" aliases (use [[senders]] in config.toml).
	// These share the active account's SMTP connection — no IMAP or credentials needed.
	Senders []SenderConfig `toml:"senders"`

	Screener ScreenerConfig `toml:"screener"`
	Folders  FoldersConfig  `toml:"folders"`
	UI       UIConfig       `toml:"ui"`

	// AutoBCC, if set, is added to every outgoing email's Bcc field so the
	// user keeps a copy in an external mailbox (e.g. their hey.com archive).
	// Format: "addr@example.com" or "Name <addr@example.com>". Shown in the
	// composer and pre-send review so it's never a silent BCC.
	AutoBCC string `toml:"auto_bcc"`
}

// ActiveAccounts returns the list of configured accounts.
// Falls back to the legacy single [account] block if [[accounts]] is empty.
func (c *Config) ActiveAccounts() []AccountConfig {
	if len(c.Accounts) > 0 {
		return c.Accounts
	}
	if c.Account.User != "" {
		return []AccountConfig{c.Account}
	}
	return nil
}

// DefaultPath returns ~/.config/neomd/config.toml.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "neomd", "config.toml")
}

// cacheDirName is derived from the config directory name (e.g. "neomd" or "neomd-demo").
// Set during Load() so that different configs use separate cache directories.
var cacheDirName = "neomd"

// HistoryPath returns the path for the command history file.
// Uses the OS cache directory (~/.cache/neomd/ on Linux) so it is never
// picked up by dotfile version control but still persists across reboots.
func HistoryPath() string {
	if dir, err := os.UserCacheDir(); err == nil {
		p := filepath.Join(dir, cacheDirName)
		_ = os.MkdirAll(p, 0700)
		return filepath.Join(p, "cmd_history")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("neomd_%d_cmd_history", os.Getuid()))
}

// welcomePath returns the path of the first-run marker file.
func welcomePath() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, cacheDirName, "welcome-shown")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("neomd_%d_welcome", os.Getuid()))
}

// IsFirstRun returns true if the welcome marker has not been written yet.
func IsFirstRun() bool {
	_, err := os.Stat(welcomePath())
	return os.IsNotExist(err)
}

// MarkWelcomeShown creates the marker so IsFirstRun returns false next time.
func MarkWelcomeShown() {
	p := welcomePath()
	_ = os.MkdirAll(filepath.Dir(p), 0700)
	_ = os.WriteFile(p, []byte("1"), 0600)
}

// Load reads config from path (or default location if path is empty).
// If no config exists, returns a placeholder config and prints a hint.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	path = expandPath(path)

	// Derive cache dir name from config directory (e.g. "neomd-demo" from
	// ~/.config/neomd-demo/config.toml) so demo and production don't share cache.
	cacheDirName = filepath.Base(filepath.Dir(path))

	cfg := defaults()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := writeDefault(path, cfg); err == nil {
			return nil, fmt.Errorf("created default config at %s — please fill in your credentials", path)
		}
		return nil, fmt.Errorf("config not found at %s", path)
	}

	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	cfg.Screener.ScreenedIn = expandPath(cfg.Screener.ScreenedIn)
	cfg.Screener.ScreenedOut = expandPath(cfg.Screener.ScreenedOut)
	cfg.Screener.Feed = expandPath(cfg.Screener.Feed)
	cfg.Screener.PaperTrail = expandPath(cfg.Screener.PaperTrail)
	cfg.Screener.Spam = expandPath(cfg.Screener.Spam)

	// Ensure screener list directories and files exist so appending (I/O/F/P/$)
	// works on a fresh install without manual mkdir or touching files.
	for _, p := range []string{
		cfg.Screener.ScreenedIn, cfg.Screener.ScreenedOut,
		cfg.Screener.Feed, cfg.Screener.PaperTrail, cfg.Screener.Spam,
	} {
		if p != "" {
			_ = os.MkdirAll(filepath.Dir(p), 0700)
			if _, err := os.Stat(p); os.IsNotExist(err) {
				_ = os.WriteFile(p, nil, 0600)
			}
		}
	}

	for i := range cfg.Accounts {
		cfg.Accounts[i].Password = expandEnv(cfg.Accounts[i].Password)
		cfg.Accounts[i].User = expandEnv(cfg.Accounts[i].User)
	}
	cfg.Account.Password = expandEnv(cfg.Account.Password)
	cfg.Account.User = expandEnv(cfg.Account.User)

	return cfg, nil
}

func defaults() *Config {
	home, _ := os.UserHomeDir()
	listsDir := filepath.Join(home, ".config", "neomd", "lists")
	return &Config{
		Accounts: []AccountConfig{
			{
				Name: "Personal",
				IMAP: "imap.example.com:993",
				SMTP: "smtp.example.com:587",
			},
		},
		Screener: ScreenerConfig{
			ScreenedIn:  filepath.Join(listsDir, "screened_in.txt"),
			ScreenedOut: filepath.Join(listsDir, "screened_out.txt"),
			Feed:        filepath.Join(listsDir, "feed.txt"),
			PaperTrail:  filepath.Join(listsDir, "papertrail.txt"),
			Spam:        filepath.Join(listsDir, "spam.txt"),
		},
		Folders: FoldersConfig{
			Inbox:       "INBOX",
			Sent:        "Sent",
			Trash:       "Trash",
			Drafts:      "Drafts",
			ToScreen:    "ToScreen",
			Feed:        "Feed",
			PaperTrail:  "PaperTrail",
			ScreenedOut: "ScreenedOut",
			Archive:     "Archive",
			Waiting:     "Waiting",
			Scheduled:   "Scheduled",
			Someday:     "Someday",
			Spam:        "Spam",
		},
		UI: UIConfig{
			Theme:          "dark",
			InboxCount:     50,
			BgSyncInterval: 5,
		},
	}
}

func writeDefault(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// expandEnv resolves a value that is entirely a single env var reference
// ($VAR or ${VAR}). If the value contains other text or multiple $ signs
// it is returned as-is, so passwords containing $ are never mangled.
func expandEnv(s string) string {
	s = strings.TrimSpace(s)
	// ${VAR} form
	if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") && strings.Count(s, "$") == 1 {
		return os.Getenv(s[2 : len(s)-1])
	}
	// $VAR form — must be a single token with no other characters
	if strings.HasPrefix(s, "$") && strings.Count(s, "$") == 1 && !strings.ContainsAny(s[1:], " \t${}") {
		return os.Getenv(s[1:])
	}
	return s
}

func expandPath(path string) string {
	if path == "" {
		return path
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		if path == "~" {
			return home
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

func TokenFilePath(accountName string) (string, error) {
	var configDir string
	if runtime.GOOS == "windows" {
		var err error
		configDir, err = os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve config directory: %w", err)
		}
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		configDir = filepath.Join(home, ".config")
	}
	safe := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '_'
		}
		return r
	}, accountName)
	return filepath.Join(configDir, cacheDirName, "tokens", safe+".json"), nil
}
