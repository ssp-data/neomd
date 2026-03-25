// Package config loads neomd configuration from ~/.config/neomd/config.toml.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// AccountConfig holds IMAP/SMTP connection settings.
type AccountConfig struct {
	Name     string `toml:"name"`
	IMAP     string `toml:"imap"`     // host:port (993 = TLS, 143 = STARTTLS)
	SMTP     string `toml:"smtp"`     // host:port (587 = STARTTLS, 465 = TLS)
	User     string `toml:"user"`
	Password string `toml:"password"`
	From     string `toml:"from"` // "Name <email@example.com>"
	STARTTLS bool   `toml:"starttls"`
}

// ScreenerConfig points to the allowlist/blocklist files.
type ScreenerConfig struct {
	ScreenedIn  string `toml:"screened_in"`
	ScreenedOut string `toml:"screened_out"`
	Feed        string `toml:"feed"`
	PaperTrail  string `toml:"papertrail"`
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
}

// UIConfig holds display preferences.
type UIConfig struct {
	Theme      string `toml:"theme"`       // dark | light | auto
	InboxCount int    `toml:"inbox_count"` // number of messages to fetch
	Signature  string `toml:"signature"`   // appended to new compose buffers (markdown)
}

// Config is the root neomd configuration.
type Config struct {
	// Accounts is the list of email accounts (use [[accounts]] in config.toml).
	// For a single account the legacy [account] block is also accepted.
	Accounts []AccountConfig `toml:"accounts"`
	Account  AccountConfig   `toml:"account"` // legacy single-account fallback

	Screener ScreenerConfig `toml:"screener"`
	Folders  FoldersConfig  `toml:"folders"`
	UI       UIConfig       `toml:"ui"`
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

// Load reads config from path (or default location if path is empty).
// If no config exists, returns a placeholder config and prints a hint.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	path = expandPath(path)

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
	muttDir := filepath.Join(home, ".config", "mutt")
	return &Config{
		Accounts: []AccountConfig{
			{
				Name: "Personal",
				IMAP: "imap.example.com:993",
				SMTP: "smtp.example.com:587",
			},
		},
		Screener: ScreenerConfig{
			ScreenedIn:  filepath.Join(muttDir, "screened_in.txt"),
			ScreenedOut: filepath.Join(muttDir, "screened_out.txt"),
			Feed:        filepath.Join(muttDir, "feed.txt"),
			PaperTrail:  filepath.Join(muttDir, "papertrail.txt"),
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
		},
		UI: UIConfig{
			Theme:      "dark",
			InboxCount: 50,
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
