# Configuration Reference

On first run, neomd creates `~/.config/neomd/config.toml` with placeholders.

## Full example

```toml
[[accounts]]
name     = "Personal"
imap     = "imap.example.com:993"   # :993 = TLS, :143 = STARTTLS
smtp     = "smtp.example.com:587"
user     = "me@example.com"
password = "app-password"
from     = "Me <me@example.com>"

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

Use an app-specific password (Gmail, Fastmail, Hostpoint, etc.) rather than your main account password.

Credentials are stored only in `~/.config/neomd/config.toml` (mode 0600) and never written elsewhere; all IMAP connections use TLS (port 993) or STARTTLS (port 143).

## Sending and Discarding

To abort a compose without sending, close neovim with `ZQ` or `:q!` (discard). To send, save normally with `ZZ` or `:wq`.

## Signature

The `signature` field in `[ui]` is appended automatically when opening a new compose buffer (`c`). It is **not** added for replies. The separator `--` is inserted for you — just write the signature body in Markdown.

Use TOML triple-quoted strings (`"""`) to preserve line breaks. The signature appears at the end of the buffer — you can edit or delete it before saving.
