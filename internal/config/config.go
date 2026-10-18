// Package config loads neomd configuration from ~/.config/neomd/config.toml.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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

// ContactsConfig points at an optional user-maintained contacts file merged
// into the harvested address→name cache at startup. Two formats are accepted:
// simple lines ("addr,name", "addr<TAB>name", or "Name <addr>") and a Google
// Contacts CSV export (contacts.google.com → Export → Google CSV).
type ContactsConfig struct {
	File string `toml:"file"`
}

// AccountConfig holds IMAP/SMTP connection settings.
type AccountConfig struct {
	Name        string `toml:"name"`
	IMAP        string `toml:"imap"` // host:port (993 = TLS, 143 = STARTTLS)
	SMTP        string `toml:"smtp"` // host:port (587 = STARTTLS, 465 = TLS)
	User        string `toml:"user"`
	Password    string `toml:"password"`
	From        string `toml:"from"` // "Name <email@example.com>"
	STARTTLS    bool   `toml:"starttls"`
	TLSCertFile string `toml:"tls_cert_file"` // optional PEM CA/cert for self-signed local bridges

	IMAPDisabled bool `toml:"imap_disabled"` // skip IMAP connection; account is send-only

	// OAuth2 fields — only used when auth_type = "oauth2".
	AuthType           string   `toml:"auth_type"` // "plain" (default) | "oauth2"
	OAuth2ClientID     string   `toml:"oauth2_client_id"`
	OAuth2ClientSecret string   `toml:"oauth2_client_secret"`
	OAuth2IssuerURL    string   `toml:"oauth2_issuer_url"` // OIDC discovery endpoint (e.g. "https://accounts.google.com")
	OAuth2AuthURL      string   `toml:"oauth2_auth_url"`   // manual override; skips discovery
	OAuth2TokenURL     string   `toml:"oauth2_token_url"`  // manual override; skips discovery
	OAuth2Scopes       []string `toml:"oauth2_scopes"`
	OAuth2RedirectPort int      `toml:"oauth2_redirect_port"` // local callback port; default 8085

	// Signature
	Signature SignatureConfig `toml:"signature_block"` // Per account Signature
}

// IsOAuth2 reports whether this account uses OAuth2 instead of password auth.
func (a AccountConfig) IsOAuth2() bool {
	return strings.EqualFold(a.AuthType, "oauth2")
}

// keyringSentinel is the literal value users put in `password = "keyring"`
// to signal "fetch from OS keyring at startup."
const keyringSentinel = "keyring"

// UseKeyring reports whether this account stores its password in the OS
// keyring. After config.Load() resolves the keyring entry, Password holds
// the actual password and this method returns false. The sentinel only
// remains if keyring lookup failed (no entry yet, or service unavailable),
// in which case the login fails until the entry is stored (service "neomd",
// key "account/<name>/password").
func (a AccountConfig) UseKeyring() bool {
	return a.Password == keyringSentinel
}

// ScreenerConfig points to the allowlist/blocklist files.
type ScreenerConfig struct {
	ScreenedIn  string `toml:"screened_in"`
	ScreenedOut string `toml:"screened_out"`
	Feed        string `toml:"feed"`
	PaperTrail  string `toml:"papertrail"`
	Spam        string `toml:"spam"`
	Notify      string `toml:"notify"` // optional: addresses or @domain entries that fire desktop notifications
}

// Theme overrides individual colour slots used by the UI. Any field left
// empty falls back to the active built-in theme value (selected via
// `[ui].theme`). All fields are hex strings, e.g. "#7E9CD8". The actual
// built-in palettes (kanagawa, kanagawa-paper, rose-pine, gruvbox,
// osaka-jade) live in internal/ui/styles.go; this struct is only the
// TOML-facing override surface.
type Theme struct {
	Bg            string `toml:"bg"`
	Border        string `toml:"border"`
	Subtle        string `toml:"subtle"`
	Selected      string `toml:"selected"`
	Text          string `toml:"text"`
	Muted         string `toml:"muted"`
	Primary       string `toml:"primary"`
	Unread        string `toml:"unread"`
	Number        string `toml:"number"`
	Date          string `toml:"date"`
	AuthorRead    string `toml:"author_read"`
	SubjectRead   string `toml:"subject_read"`
	SizeCol       string `toml:"size_col"`
	AuthorUnread  string `toml:"author_unread"`
	SubjectUnread string `toml:"subject_unread"`
	Error         string `toml:"error"`
	Success       string `toml:"success"`
}

// CalendarConfig configures local handoff for iCalendar invites. The reader
// shows a card whenever an email contains a `text/calendar` part or `.ics`
// attachment. The leader chord `<space> v {a|d|t}` sends an iMIP RSVP reply;
// `<space> v o` writes the .ics to a cache file and runs OpenCommand against
// it, letting the user import the event into their local calendar app
// (default `xdg-open` follows the system's MIME handler; set to `morgen`,
// `khal`, etc. to force a specific app).
type CalendarConfig struct {
	OpenCommand string `toml:"open_command"` // default "xdg-open"
}

// AIConfig configures the pre-send "AI handoff" key (`i`). When pressed,
// neomd shows a one-line prompt for an instruction (e.g. "fix grammar"),
// writes the current draft to a temp markdown file with the standard
// `# [neomd: ...]` headers, spawns `<command> [args...]` with cwd set to
// the file's directory, and re-reads the file on exit so any changes
// round-trip back into the draft. Quit the AI tool to return.
//
// Two placeholders are substituted in args at spawn time:
//   - `{prompt}` → the typed instruction (or empty for interactive mode)
//   - `{file}`   → the draft's basename (cwd is set to its directory)
//
// Default args = ["edit {file}: {prompt}"]. With prompt "fix grammar" the
// spawn is `claude "edit neomd-ai-XYZ.md: fix grammar"` (cwd /tmp/neomd) —
// claude finds the file via cwd and edits in place. Set `command = ""` to
// disable the binding.
type AIConfig struct {
	Command string   `toml:"command"`
	Args    []string `toml:"args"` // {prompt} and {file} placeholders are substituted
}

// NotificationsConfig controls desktop notifications for emails landing in
// folders the user cares about, scoped to senders listed in screener.notify.
// TUI-only: the headless daemon never fires notifications.
type NotificationsConfig struct {
	Enabled  bool     `toml:"enabled"`   // opt-in, default false
	Command  string   `toml:"command"`   // notify binary, default "notify-send"
	Icon     string   `toml:"icon"`      // -i/--icon arg, default "mail-message-new"
	ExpireMs int      `toml:"expire_ms"` // -t arg in milliseconds, default 5000
	Folders  []string `toml:"folders"`   // folder labels (e.g. "Inbox") to fire on; default ["Inbox"]
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
	Work        string `toml:"work"`
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
	"work":         "Work",
}

// LabelFor returns the UI label (e.g. "Inbox", "PaperTrail") for a configured
// IMAP folder name. Useful for matching user-facing config (which uses labels)
// against runtime values (which use IMAP names — they may differ, e.g. Gmail's
// "[Gmail]/All Mail" or HEY's "HEY/Paper Trail"). Returns the input
// unchanged if no mapping exists, so the caller can fall back to direct
// string comparison.
func (f FoldersConfig) LabelFor(imapName string) string {
	switch imapName {
	case f.Inbox:
		return "Inbox"
	case f.Sent:
		return "Sent"
	case f.Trash:
		return "Trash"
	case f.Drafts:
		return "Drafts"
	case f.ToScreen:
		return "ToScreen"
	case f.Feed:
		return "Feed"
	case f.PaperTrail:
		return "PaperTrail"
	case f.ScreenedOut:
		return "ScreenedOut"
	case f.Archive:
		return "Archive"
	case f.Waiting:
		return "Waiting"
	case f.Scheduled:
		return "Scheduled"
	case f.Someday:
		return "Someday"
	case f.Spam:
		return "Spam"
	case f.Work:
		return "Work"
	}
	return imapName
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

// SignatureConfig holds plain text and HTML signature blocks.
type SignatureConfig struct {
	Text string `toml:"text"` // markdown/plain text signature for text/plain part and editor
	HTML string `toml:"html"` // optional HTML signature injected into text/html part
}

// UIConfig holds display preferences.
type UIConfig struct {
	Theme                 string          `toml:"theme"`                   // dark | light | auto
	InboxCount            int             `toml:"inbox_count"`             // number of messages to fetch
	Signature             string          `toml:"signature"`               // legacy: plain signature (markdown). Deprecated in favor of [ui.signature] block.
	SignatureBlock        SignatureConfig `toml:"signature_block"`         // new structured signature config
	AutoScreenOnLoad      *bool           `toml:"auto_screen_on_load"`     // screen inbox on every load (default true)
	BgSyncInterval        int             `toml:"bg_sync_interval"`        // background sync interval in minutes (0 = disabled, default 5)
	BulkProgressThreshold int             `toml:"bulk_progress_threshold"` // show progress counter for batches larger than this (default 10)
	DraftBackupCount      int             `toml:"draft_backup_count"`      // rolling compose backups in ~/.cache/neomd/drafts/ (default 20, -1 = disabled)
	MarkAsReadAfterSecs   int             `toml:"mark_as_read_after_secs"` // seconds in reader before marking as read (0 = immediate, default 7)
	InstantFolderSwitch   *bool           `toml:"instant_folder_switch"`   // show the last-seen list at once and refresh behind it (default true)
}

// DraftBackups returns the max number of rolling draft backups (default 20, -1 = disabled).
func (u UIConfig) DraftBackups() int {
	if u.DraftBackupCount == 0 {
		return 20
	}
	return u.DraftBackupCount
}

// BulkThreshold returns the configured bulk progress threshold (default 10).
func (u UIConfig) BulkThreshold() int {
	if u.BulkProgressThreshold <= 0 {
		return 10
	}
	return u.BulkProgressThreshold
}

// InstantSwitch reports whether folder switches show the cached list first
// (default true). false restores the spinner-on-every-switch behavior.
func (u UIConfig) InstantSwitch() bool {
	if u.InstantFolderSwitch == nil {
		return true
	}
	return *u.InstantFolderSwitch
}

// AutoScreen returns true if auto-screen-on-inbox-load is enabled (default: true).
func (u UIConfig) AutoScreen() bool {
	if u.AutoScreenOnLoad == nil {
		return true
	}
	return *u.AutoScreenOnLoad
}

// TagsConfig holds the IMAP keyword tagging feature ([tags] in config.toml).
// Tags are stored as IMAP keywords on the message (server-side, visible to
// other clients); <config dir>/tags/ is the local write-through registry.
type TagsConfig struct {
	// Enabled is the master switch — opt-in: the feature is OFF until the
	// config says `enabled = true`. false (or an absent [tags] section)
	// leaves the <Key> binding inert AND renders no chips at all (a global
	// kill-switch that per-tag [tags.<keyword>] enabled values cannot
	// override — those only hide single keywords while the feature is on).
	Enabled bool `toml:"enabled"`
	// Key is the leader-chord key that opens the tag picker (default "k",
	// i.e. <space>k). Matched in handleChord against the chord key.
	Key string `toml:"key"`
	// NerdPill renders chips as nerd-font "pills" — half-sphere end caps
	// (U+E0B6/U+E0B4) coloured like the chip — instead of the padded-text
	// fallback. Needs a nerd font in the terminal; default false.
	NerdPill bool `toml:"nerd_pill"`
	// FG/BG are the default chip colours — the fallback for every tag
	// without its own [tags.<keyword>] section. Values: hex ("#1F1F28"), an
	// ANSI number ("21"), or "transparent"/"none" for bg (emits no
	// background, so a transparent terminal shows through; pill caps then
	// draw in the fg colour). Empty = reverse video of the active theme
	// (Background=theme Text, Foreground=theme Bg); anything else falls back
	// with a startup warning. The built-in palettes are never touched.
	FG string `toml:"fg"`
	BG string `toml:"bg"`
	// Rules holds the per-tag [tags.<keyword>] sections (display name,
	// colours, visibility). They are sub-tables of [tags], invisible to the
	// struct decode, so Load() fills this map with a second raw pass — not a
	// TOML field of its own.
	Rules map[string]TagRule `toml:"-"`
}

// TagRule is one per-tag override, written as [tags.<keyword>] in
// config.toml. It only affects how that tag's chip DISPLAYS — the keyword
// stored on the server is never renamed or removed by it.
type TagRule struct {
	// Enabled controls the chip's visibility: false hides every chip for
	// this keyword (the tag itself stays on the server and in the picker,
	// it just stops rendering); true (or omitted) shows it.
	Enabled *bool `toml:"enabled"`
	// Display is the chip text. May contain nerd-font symbols and spaces —
	// it is display-only, the keyword saved on the server is unchanged.
	// Empty = the keyword itself.
	Display string `toml:"display"`
	// FG/BG override the colours for this tag; empty fields fall back to the
	// [tags] fg/bg defaults, then to the theme's reverse video. Same value
	// formats as [tags] fg/bg (hex, ANSI number, bg "transparent").
	FG string `toml:"fg"`
	BG string `toml:"bg"`
}

// Shown reports whether the tag's chips render (default true — writing a
// per-tag section customizes, only enabled = false hides).
func (r TagRule) Shown() bool { return r.Enabled == nil || *r.Enabled }

// Rule returns the per-tag section for keyword (case-insensitive, IMAP
// keywords compare case-insensitively).
func (t TagsConfig) Rule(keyword string) (TagRule, bool) {
	if t.Rules == nil || keyword == "" {
		return TagRule{}, false
	}
	for name, rule := range t.Rules {
		if strings.EqualFold(name, keyword) {
			return rule, true
		}
	}
	return TagRule{}, false
}

// TagsKey returns the picker's leader-chord key (default "k").
func (t TagsConfig) TagsKey() string {
	if t.Key == "" {
		return "k"
	}
	return t.Key
}

// parseTagRules extracts the [tags.<keyword>] sub-tables from config.toml.
// The regular struct decode cannot see them (they would need one struct
// field per tag), so this walks the raw TOML tree: every key under [tags]
// whose value is a table becomes a TagRule, keyed lowercased. Scalar keys
// (enabled, key, fg, bg of [tags] itself) are skipped by the is-table check.
func parseTagRules(path string) map[string]TagRule {
	raw := map[string]any{}
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return nil
	}
	tags, ok := raw["tags"].(map[string]any)
	if !ok {
		return nil
	}
	var rules map[string]TagRule
	for name, val := range tags {
		tbl, ok := val.(map[string]any)
		if !ok {
			continue // [tags] own scalar settings
		}
		if rules == nil {
			rules = make(map[string]TagRule)
		}
		r := TagRule{}
		if b, ok := tbl["enabled"].(bool); ok {
			r.Enabled = &b
		}
		r.Display, _ = tbl["display"].(string)
		r.FG, _ = tbl["fg"].(string)
		r.BG, _ = tbl["bg"].(string)
		rules[strings.ToLower(name)] = r
	}
	return rules
}

// Resolved returns a copy with sensible fallbacks filled in for any field
// the user enabled-but-left-blank. Safe to call when Enabled is false.
func (n NotificationsConfig) Resolved() NotificationsConfig {
	out := n
	if out.Command == "" {
		out.Command = "notify-send"
	}
	if out.Icon == "" {
		out.Icon = "mail-message-new"
	}
	if out.ExpireMs <= 0 {
		out.ExpireMs = 5000
	}
	if len(out.Folders) == 0 {
		out.Folders = []string{"Inbox"}
	}
	return out
}

// FolderAllowed reports whether folder is in the configured Folders list
// (case-insensitive, with sensible defaults applied).
func (n NotificationsConfig) FolderAllowed(folder string) bool {
	r := n.Resolved()
	for _, f := range r.Folders {
		if strings.EqualFold(f, folder) {
			return true
		}
	}
	return false
}

// Config is the root neomd configuration.
type Config struct {
	// Accounts is the list of email accounts (use [[accounts]] in config.toml).
	// For a single account the legacy [account] block is also accepted.
	Accounts []AccountConfig `toml:"accounts"`
	Account  AccountConfig   `toml:"account"` // legacy single-account fallback

	// StoreSentDraftsInSendingAccount controls where Sent/Drafts are stored when
	// multiple SMTP identities are configured. Default false: always use the
	// primary IMAP account (the first configured account). When true, Sent/Drafts
	// follow the selected sending account.
	StoreSentDraftsInSendingAccount bool `toml:"store_sent_drafts_in_sending_account"`

	// Senders is a list of extra "From" aliases (use [[senders]] in config.toml).
	// These share the active account's SMTP connection — no IMAP or credentials needed.
	Senders []SenderConfig `toml:"senders"`

	Screener      ScreenerConfig      `toml:"screener"`
	Contacts      ContactsConfig      `toml:"contacts"`
	Folders       FoldersConfig       `toml:"folders"`
	UI            UIConfig            `toml:"ui"`
	Notifications NotificationsConfig `toml:"notifications"`
	AI            AIConfig            `toml:"ai"`
	Theme         Theme               `toml:"theme"`
	Calendar      CalendarConfig      `toml:"calendar"`

	// AutoBCC, if set, is added to every outgoing email's Bcc field so the
	// user keeps a copy in an external mailbox (e.g. their hey.com archive).
	// Format: "addr@example.com" or "Name <addr@example.com>". Shown in the
	// composer and pre-send review so it's never a silent BCC.
	AutoBCC string `toml:"auto_bcc"`

	// DefaultFrom, if set, is the email address (bare or "Name <addr>") that
	// new compose and no-match reply/forward should default the From field
	// to, overriding the usual "first configured account" fallback. Matched
	// against accounts first, then senders. Empty (the default) preserves
	// prior behavior: the first account wins.
	DefaultFrom string `toml:"default_from"`

	Listmonk ListmonkConfig `toml:"listmonk"`

	// Tags configures IMAP keyword tagging ([tags] in config.toml).
	Tags TagsConfig `toml:"tags"`

	// OOO configures out-of-office auto-replies, processed by the headless
	// daemon (--headless) only. Replies go solely to screened-in senders,
	// once per sender per OOO period.
	OOO OOOConfig `toml:"ooo"`

	// OOOFile is the path to the optional ooo.toml sitting next to the
	// loaded config.toml. When that file exists its contents replace the
	// whole [ooo] block — it is the single file `make ooo` syncs to the
	// headless server, and the daemon re-reads it every pass (hot reload,
	// no restart needed). Set during Load(), not a TOML field.
	OOOFile string `toml:"-"`
	// MergesFile is <config dir>/merges.toml — user-defined merged threads
	// (see internal/merge). Set during Load(), not a TOML field.
	MergesFile string `toml:"-"`
	// TagsDir is <config dir>/tags/ — the local registry of IMAP keyword
	// tags (see internal/tags): one folder per account, one line-based file
	// per keyword (the write-through backup of server-side tags; the split
	// keeps a later restore a per-account operation). Set during Load(),
	// not a TOML field.
	TagsDir string `toml:"-"`
}

// OOOConfig holds out-of-office auto-reply settings ([ooo] in config.toml).
// Only the headless daemon acts on it; the TUI ignores this block.
type OOOConfig struct {
	Enabled  bool     `toml:"enabled"`
	Accounts []string `toml:"accounts"`  // [[accounts]] names whose inboxes get auto-replies, each from its own address (e.g. ["Work", "WorkInfo"]); empty = the daemon's own account
	Timezone string   `toml:"timezone"`  // IANA name (e.g. "Europe/Zurich") that from/until are interpreted in; empty = the daemon machine's local time
	From     string   `toml:"from"`      // "YYYY-MM-DD" — active starting at 00:00 of this day (local time); empty = active immediately
	Until    string   `toml:"until"`     // "YYYY-MM-DD" — active through the END of this day (local time); empty = active until enabled=false
	Subject  string   `toml:"subject"`   // reply subject; default "Out of Office"
	Body     string   `toml:"body"`      // reply body in markdown (same rendering as composed emails)
	BodyFile string   `toml:"body_file"` // optional path to a markdown file; overrides body when set
}

// ListmonkTrigger maps a virtual email address to Listmonk list IDs.
// TemplateID, when non-zero, overrides Listmonk's default template for the
// generated campaign (e.g. a per-list "Book Update" template).
type ListmonkTrigger struct {
	Address    string `toml:"address"`
	ListIDs    []int  `toml:"list_ids"`
	TemplateID int    `toml:"template_id"`
}

// ListmonkConfig holds settings for the Listmonk newsletter integration.
type ListmonkConfig struct {
	URL          string            `toml:"url"`
	APIUser      string            `toml:"api_user"`
	APIToken     string            `toml:"api_token"`
	DelayMinutes int               `toml:"delay_minutes"`
	Triggers     []ListmonkTrigger `toml:"triggers"`
}

// ListmonkEnabled returns true if Listmonk integration is configured.
func (c *Config) ListmonkEnabled() bool {
	return c.Listmonk.URL != "" && len(c.Listmonk.Triggers) > 0
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

// Signature resolves what signature to use and returns a SignatureConfig.
// It's up to the caller to decide to use either HTML or Text. The function
// resolves first to dedicated account signature if defined, else the UI signature
func (c *Config) Signature(a AccountConfig) SignatureConfig {
	if a.Signature.Text != "" || a.Signature.HTML != "" {
		return a.Signature
	}
	if c.UI.SignatureBlock.Text != "" || c.UI.SignatureBlock.HTML != "" {
		return c.UI.SignatureBlock
	}
	return SignatureConfig{Text: c.UI.Signature}
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
		_ = os.MkdirAll(p, 0o700)
		return filepath.Join(p, "cmd_history")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("neomd_%d_cmd_history", os.Getuid()))
}

// AuditLogPath returns ~/.cache/neomd/moves.log — every server-side MOVE and
// EXPUNGE neomd performs (TUI, daemon, CLI) is appended there.
func AuditLogPath() string {
	if dir, err := os.UserCacheDir(); err == nil {
		p := filepath.Join(dir, cacheDirName)
		_ = os.MkdirAll(p, 0o700)
		return filepath.Join(p, "moves.log")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("neomd_%d_moves.log", os.Getuid()))
}

// IMAPTracePath returns ~/.cache/neomd/imap-trace.log, the opt-in
// (NEOMD_IMAP_TRACE=1) per-operation timing log. Same directory as moves.log.
func IMAPTracePath() string {
	if dir, err := os.UserCacheDir(); err == nil {
		p := filepath.Join(dir, cacheDirName)
		_ = os.MkdirAll(p, 0o700)
		return filepath.Join(p, "imap-trace.log")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("neomd_%d_imap-trace.log", os.Getuid()))
}

// InlineImageDir returns ~/.cache/neomd/inline/, creating it if needed. Reply
// and forward write the quoted mail's inline (cid:) images here so the send
// pipeline can re-embed them like any local image.
func InlineImageDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		p := filepath.Join(dir, cacheDirName, "inline")
		_ = os.MkdirAll(p, 0o700)
		return p
	}
	p := filepath.Join(os.TempDir(), fmt.Sprintf("neomd_%d_inline", os.Getuid()))
	_ = os.MkdirAll(p, 0o700)
	return p
}

// DraftsBackupDir returns ~/.cache/neomd/drafts/, creating it if needed.
func DraftsBackupDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		p := filepath.Join(dir, cacheDirName, "drafts")
		_ = os.MkdirAll(p, 0o700)
		return p
	}
	p := filepath.Join(os.TempDir(), fmt.Sprintf("neomd_%d_drafts", os.Getuid()))
	_ = os.MkdirAll(p, 0o700)
	return p
}

// CrashLogPath returns the path for the crash log file.
func CrashLogPath() string {
	if dir, err := os.UserCacheDir(); err == nil {
		p := filepath.Join(dir, cacheDirName)
		_ = os.MkdirAll(p, 0o700)
		return filepath.Join(p, "crash.log")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("neomd_%d_crash.log", os.Getuid()))
}

// LoadOOOOverride parses an ooo.toml file holding a bare OOO block (top-level
// enabled/until/subject/body/body_file keys). Returns nil when the file does
// not exist, an error when it exists but is invalid TOML — the daemon logs
// that and fails safe (no replies).
func LoadOOOOverride(path string) (*OOOConfig, error) {
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	var o OOOConfig
	if _, err := toml.DecodeFile(path, &o); err != nil {
		return nil, fmt.Errorf("parse ooo config %s: %w", path, err)
	}
	o.BodyFile = expandPath(o.BodyFile)
	return &o, nil
}

// OOOCachePath returns the path for the out-of-office reply cache file
// (which senders already received an auto-reply in the current OOO period).
func OOOCachePath() string {
	if dir, err := os.UserCacheDir(); err == nil {
		p := filepath.Join(dir, cacheDirName)
		_ = os.MkdirAll(p, 0o700)
		return filepath.Join(p, "ooo_replied")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("neomd_%d_ooo_replied", os.Getuid()))
}

// SpyPixelCachePath returns the path for the spy pixel cache file.
func SpyPixelCachePath() string {
	if dir, err := os.UserCacheDir(); err == nil {
		p := filepath.Join(dir, cacheDirName)
		_ = os.MkdirAll(p, 0o700)
		return filepath.Join(p, "spy_pixels")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("neomd_%d_spy_pixels", os.Getuid()))
}

// ContactsCachePath returns the path for the harvested address→name cache
// used to match name searches and decorate outgoing To/Cc headers.
func ContactsCachePath() string {
	if dir, err := os.UserCacheDir(); err == nil {
		p := filepath.Join(dir, cacheDirName)
		_ = os.MkdirAll(p, 0o700)
		return filepath.Join(p, "contacts")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("neomd_%d_contacts", os.Getuid()))
}

// NotifyStatePath returns the path for the per-folder last-seen-UID baseline
// used by the notification system to decide which messages count as "new".
func NotifyStatePath() string {
	if dir, err := os.UserCacheDir(); err == nil {
		p := filepath.Join(dir, cacheDirName)
		_ = os.MkdirAll(p, 0o700)
		return filepath.Join(p, "notify_state.json")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("neomd_%d_notify_state.json", os.Getuid()))
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
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte("1"), 0o600)
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
	cfg.Screener.Notify = expandPath(cfg.Screener.Notify)
	cfg.Contacts.File = expandPath(cfg.Contacts.File)
	cfg.OOO.BodyFile = expandPath(cfg.OOO.BodyFile)

	// ooo.toml next to config.toml replaces the whole [ooo] block when it
	// exists (single syncable file; the daemon also re-reads it every pass).
	cfg.OOOFile = filepath.Join(filepath.Dir(path), "ooo.toml")
	cfg.MergesFile = filepath.Join(filepath.Dir(path), "merges.toml")
	cfg.TagsDir = filepath.Join(filepath.Dir(path), "tags")
	cfg.Tags.Rules = parseTagRules(path)
	if override, err := LoadOOOOverride(cfg.OOOFile); err != nil {
		return nil, err
	} else if override != nil {
		cfg.OOO = *override
	}

	// Ensure screener list directories and files exist so appending (I/O/F/P/$)
	// works on a fresh install without manual mkdir or touching files.
	for _, p := range []string{
		cfg.Screener.ScreenedIn, cfg.Screener.ScreenedOut,
		cfg.Screener.Feed, cfg.Screener.PaperTrail, cfg.Screener.Spam,
		cfg.Screener.Notify,
	} {
		if p != "" {
			_ = os.MkdirAll(filepath.Dir(p), 0o700)
			if _, err := os.Stat(p); os.IsNotExist(err) {
				_ = os.WriteFile(p, nil, 0o600)
			}
		}
	}

	for i := range cfg.Accounts {
		cfg.Accounts[i].Password = expandEnv(cfg.Accounts[i].Password)
		cfg.Accounts[i].User = expandEnv(cfg.Accounts[i].User)
		cfg.Accounts[i].TLSCertFile = expandPath(expandEnv(cfg.Accounts[i].TLSCertFile))
		cfg.Accounts[i].Password = resolveKeyringPassword(cfg.Accounts[i].Name, cfg.Accounts[i].Password)
	}
	cfg.Account.Password = expandEnv(cfg.Account.Password)
	cfg.Account.User = expandEnv(cfg.Account.User)
	cfg.Account.TLSCertFile = expandPath(expandEnv(cfg.Account.TLSCertFile))
	cfg.Account.Password = resolveKeyringPassword(cfg.Account.Name, cfg.Account.Password)

	cfg.Listmonk.APIToken = expandEnv(cfg.Listmonk.APIToken)

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}

	return cfg, nil
}

// validate checks config values for common mistakes.
func (cfg *Config) validate() error {
	if len(cfg.Accounts) == 0 && cfg.Account.IMAP == "" {
		return fmt.Errorf("no accounts configured — add at least one [[accounts]] section")
	}
	for i, a := range cfg.Accounts {
		label := a.Name
		if label == "" {
			label = fmt.Sprintf("accounts[%d]", i)
		}
		if !a.IMAPDisabled {
			if a.IMAP == "" {
				return fmt.Errorf("account %q: imap address is required", label)
			}
			if err := validateHostPort(a.IMAP, label, "imap"); err != nil {
				return err
			}
		}
		if a.SMTP == "" {
			return fmt.Errorf("account %q: smtp address is required", label)
		}
		if err := validateHostPort(a.SMTP, label, "smtp"); err != nil {
			return err
		}
		if a.User == "" && !a.IsOAuth2() {
			return fmt.Errorf("account %q: user is required", label)
		}
	}
	// Validate legacy single-account fields if used
	if cfg.Account.IMAP != "" {
		if err := validateHostPort(cfg.Account.IMAP, "account", "imap"); err != nil {
			return err
		}
		if cfg.Account.SMTP != "" {
			if err := validateHostPort(cfg.Account.SMTP, "account", "smtp"); err != nil {
				return err
			}
		}
	}
	// Validate UI settings
	if cfg.UI.InboxCount < 0 {
		return fmt.Errorf("ui.inbox_count must be >= 0, got %d", cfg.UI.InboxCount)
	}
	if cfg.UI.BgSyncInterval < 0 {
		return fmt.Errorf("ui.bg_sync_interval must be >= 0, got %d", cfg.UI.BgSyncInterval)
	}
	if cfg.UI.MarkAsReadAfterSecs < 0 {
		return fmt.Errorf("ui.mark_as_read_after_secs must be >= 0, got %d", cfg.UI.MarkAsReadAfterSecs)
	}
	return nil
}

// validateHostPort checks that an address is in host:port format with a valid port.
func validateHostPort(addr, account, field string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("account %q: %s %q is not valid host:port — %w", account, field, addr, err)
	}
	if host == "" {
		return fmt.Errorf("account %q: %s host is empty in %q", account, field, addr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("account %q: %s port %q is not a valid port (1-65535)", account, field, portStr)
	}
	return nil
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
			Notify:      filepath.Join(listsDir, "notify.txt"),
		},
		Notifications: NotificationsConfig{
			Enabled:  false,
			Command:  "notify-send",
			Icon:     "mail-message-new",
			ExpireMs: 5000,
			Folders:  []string{"Inbox"},
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
			Theme:               "kanagawa", // built-in: kanagawa | kanagawa-paper | kanagawa-light | rose-pine | gruvbox | osaka-jade
			InboxCount:          200,
			BgSyncInterval:      5,
			MarkAsReadAfterSecs: 7,
			Signature:           "*sent from [neomd](https://neomd.ssp.sh)*",
		},
		Tags: TagsConfig{
			// Opt-in feature: off until the user sets `enabled = true`.
			// Written into the generated default config so the option is
			// discoverable.
			Enabled: false,
			Key:     "k", // <space>k opens the tag picker once enabled
		},
		AI: AIConfig{
			// Default: hand off to Claude Code in **interactive** mode (not
			// `-p` print mode, which would bill against Anthropic API credits
			// instead of a Pro/Max subscription).
			//
			// `{prompt}` is the user's typed instruction. `{file}` is the
			// basename of the temp draft. neomd sets the spawned command's
			// cwd to the file's directory, so claude can reach the file
			// natively via its Edit tool without --add-dir tricks.
			//
			// With this default, typing "fix grammar" at the AI prompt
			// produces `claude "edit neomd-ai-XYZ.md: fix grammar"` (cwd
			// set), and claude edits the file in place.
			Command: "claude",
			Args:    []string{"edit {file}: {prompt}"},
		},
	}
}

func writeDefault(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// resolveKeyringPassword turns the "keyring" sentinel into the actual password
// stored in the OS keyring under the given account name. Anything else is
// returned unchanged. If the keyring lookup fails (no entry yet, or service
// unavailable) the sentinel is preserved so downstream code can detect it and
// prompt the user; a one-line warning is written to stderr.
//
// This step runs in Load() so every consumer (IMAP at boot, SMTP at send,
// senders aliasing this account) sees the resolved value.
func resolveKeyringPassword(accountName, password string) string {
	if password != keyringSentinel || accountName == "" {
		return password
	}
	resolved, err := keyring.GetPassword(accountName)
	if err == nil {
		return resolved
	}
	if err == keyring.ErrNotFound {
		fmt.Fprintf(os.Stderr, "neomd: account %q: keyring entry not set — store the password in the OS keyring under service \"neomd\", key \"account/%s/password\" (docs: Configuration → Storing passwords in the OS keyring)\n", accountName, accountName)
	} else {
		fmt.Fprintf(os.Stderr, "neomd: account %q: keyring unavailable: %v\n", accountName, err)
	}
	return password // leave sentinel for downstream
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
