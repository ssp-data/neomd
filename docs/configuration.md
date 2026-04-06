# Configuration Reference

On first run, neomd creates `~/.config/neomd/config.toml` with placeholders.

## OS Keyring Support

Neomd supports storing passwords and OAuth2 tokens in your operating system's secure keyring instead of the config file. This is more secure as credentials are encrypted and not stored in plaintext.

**Supported keyrings:**
- **macOS**: Keychain
- **Linux**: Secret Service (GNOME Keyring, KDE Wallet, etc.)
- **Windows**: Credential Manager

### Password Storage

For password-based authentication, set `password = "keyring"` in your account configuration:

```toml
[[accounts]]
name     = "Personal"
imap     = "imap.example.com:993"
smtp     = "smtp.example.com:587"
user     = "me@example.com"
password = "keyring"  # Fetch from OS keyring
from     = "Me <me@example.com>"
```

### OAuth2 Token Storage (Automatic)

**OAuth2 tokens automatically use the keyring by default** — no configuration needed! The token is stored securely in your OS keyring.

If the keyring is unavailable (e.g., headless/SSH environment), neomd automatically falls back to file storage in `~/.config/neomd/tokens/<account>.json`.

### Setting Passwords in the Keyring

**Option 1: Use the TUI command** (recommended)
1. Start neomd
2. Type `:set-password <account>` or `:sp`
3. Enter your password when prompted

**Option 2: Migrate from existing config**
1. If you have plaintext passwords in your config, run `:migrate-to-keyring` or `:mtk`
2. This will move all passwords and OAuth2 tokens to the keyring and update your config

**Option 3: Manual setup via command line**
```bash
# Build and run the keyring helper (if available in your installation)
neomd --set-password Personal
```

### Commands

- `:set-password` or `:sp` — Update password for current account in keyring
- `:migrate-to-keyring` or `:mtk` — Migrate all plaintext credentials to keyring

### Keyring Storage Details

- Passwords are stored as: `neomd/account/<name>/password`
- OAuth2 tokens are stored as: `neomd/account/<name>/oauth2`

Both are encrypted by your OS keyring and are only accessible to your user account.

## Full example

```toml
[[accounts]]
name     = "Personal"
imap     = "imap.example.com:993"   # :993 = TLS, :143 = STARTTLS
smtp     = "smtp.example.com:587"
user     = "me@example.com"
password = "keyring"                  # Use OS keyring for secure storage
from     = "Me <me@example.com>"

# OAuth2 authenticated accounts also support keyring storage
[[accounts]]
name     = "Work"
imap     = "imap.gmail.com:993"
smtp     = "smtp.gmail.com:587"
user     = "me@work.com"
from     = "Me <me@work.com>"
password = "keyring"                  # OAuth2 tokens stored in keyring
auth_type = "oauth2"
oauth2_client_id = "your-client-id"
oauth2_client_secret = "your-client-secret"
oauth2_issuer_url = "https://accounts.google.com"
oauth2_scopes = ["https://mail.google.com/", "offline_access"]

# Multiple accounts supported — add more [[accounts]] blocks
# Switch between them with `ctrl+a` in the inbox

# Optional: SMTP-only aliases — cycle with ctrl+f in compose/pre-send
# [[senders]]
# name    = "Work alias"
# from    = "info@example.com"
# account = "Personal"   # must match the name = field of an [[accounts]] block

[screener]
# default: ~/.config/neomd/lists/ — or reuse existing neomutt lists
screened_in  = "~/.config/neomd/lists/screened_in.txt"
screened_out = "~/.config/neomd/lists/screened_out.txt"
feed         = "~/.config/neomd/lists/feed.txt"
papertrail   = "~/.config/neomd/lists/papertrail.txt"
spam         = "~/.config/neomd/lists/spam.txt"

[folders]
inbox        = "INBOX"
sent         = "Sent"
trash        = "Trash"
drafts       = "Drafts"
to_screen    = "ToScreen"
feed         = "Feed"
papertrail   = "PaperTrail"
screened_out = "ScreenedOut"
archive      = "Archive"
waiting      = "Waiting"
scheduled    = "Scheduled"
someday      = "Someday"
spam         = "spam" #check capitalization of your pre-existing Spam folder, sometimes might be `Spam` with `S`
# tab_order controls the left-to-right tab sequence; omit to use the built-in default order. e.g.:
# tab_order = ["inbox", "to_screen", "feed", "papertrail", "waiting", "someday", "scheduled", "sent", "archive", "screened_out", "drafts", "trash"]
# Gmail uses different folder names — see docs/gmail.md for the correct mapping.

[ui]
theme                = "dark"   # dark | light | auto
inbox_count          = 50
auto_screen_on_load  = true     # screen inbox automatically on every load (default true)
bg_sync_interval     = 5        # background sync interval in minutes; 0 = disabled (default 5)
bulk_progress_threshold = 10    # show progress counter for batch operations larger than this (default 10)
signature   = """**Your Name**
Your Title, Your Company

Connect: [LinkedIn](https://example.com/)"""
```

> **Gmail users:** Gmail uses different IMAP folder names (`[Gmail]/Sent Mail`, `[Gmail]/Trash`, etc.). See [Gmail Configuration](gmail.md) for the correct mapping.

Use an app-specific password (Gmail, Fastmail, Hostpoint, etc.) rather than your main account password.

**Security note:** When using `password = "keyring"`, credentials are stored only in your OS keyring (encrypted by the OS). When using plaintext passwords, they are stored in `~/.config/neomd/config.toml` (mode 0600). All IMAP connections use TLS (port 993) or STARTTLS (port 143).

## Sending and Discarding

To abort a compose without sending, close neovim with `ZQ` or `:q!` (discard). To send, save normally with `ZZ` or `:wq`.

## Signature

The `signature` field in `[ui]` is appended automatically when opening a new compose buffer (`c`). It is **not** added for replies. The separator `-- ` is inserted for you — just write the signature body in Markdown.

Use TOML triple-quoted strings (`"""`) to preserve line breaks. The signature appears at the end of the buffer — you can edit or delete it before saving.

## OAuth2 Authentication

Neomd supports OAuth2 authenticated accounts. Add `oauth2_client_id`, `oauth2_client_secret`, `oauth2_scopes` and `oauth2_issuer_url` to your account configuration.

**OAuth2 tokens are automatically stored in the OS keyring by default.** No `password` field or additional configuration is required. If the keyring is unavailable (e.g., headless/SSH environment), neomd gracefully falls back to file storage.

### Example OAuth2 Configuration

```toml
[[accounts]]
name     = "Work"
imap     = "imap.gmail.com:993"
smtp     = "smtp.gmail.com:587"
user     = "me@work.com"
from     = "Me <me@work.com>"
auth_type = "oauth2"
oauth2_client_id = "your-client-id"
oauth2_client_secret = "your-client-secret"
oauth2_issuer_url = "https://accounts.google.com"
oauth2_scopes = ["https://mail.google.com/", "offline_access"]
```

### Issuer URL

By default, if an issuer URL is provided (e.g., `https://login.microsoftonline.com/common/v2.0` for Office365 accounts), neomd will search for the OpenID Connect discovery URL (`/.well-known/openid-configuration`) to resolve the `oauth2_token_url` and `oauth2_auth_url`. These parameters can also be provided manually.

### Scopes

The scopes required depend on your provider. For example, Office365 accounts need:
```
"https://outlook.office365.com/IMAP.AccessAsUser.All", "offline_access"
```

### Reference Documentation

- [Microsoft OAuth2 for Office365](https://learn.microsoft.com/en-us/exchange/client-development/legacy-protocols/authenticate-an-imap-pop-smtp-application-by-using-oauth)
- [Google OAuth2 for Gmail](https://developers.google.com/workspace/gmail/imap/xoauth2-protocol)
