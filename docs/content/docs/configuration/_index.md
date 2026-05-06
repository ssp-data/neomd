---
title: Configurations
weight: 1
sidebar:
  open: false
---

On first run, neomd creates `~/.config/neomd/config.toml` with placeholders.


## Full example

```toml
[[accounts]]
name     = "Personal"
imap     = "imap.example.com:993"   # :993 = TLS, :143 = STARTTLS
smtp     = "smtp.example.com:587"   # :587 = STARTTLS, :465 = TLS
user     = "me@example.com"
password = "app-password"
from     = "Me <me@example.com>"
starttls = false                    # optional: force STARTTLS (see TLS/STARTTLS section below)
tls_cert_file = ""                  # optional PEM cert/CA for self-signed local bridges
imap_disabled = false               # set true for send-only accounts (no IMAP connection)

# OAuth2 authenticated accounts are supported, it just need the relevant fields. Note that the password field is not required.
[[accounts]]
name     = "Personal"
imap     = "imap.example.com:993"   # :993 = TLS, :143 = STARTTLS
smtp     = "smtp.example.com:587"
user     = "me@example.com"
from     = "Me <me@example.com>"
oauth2_client_id = ""
oauth2_client_secret = ""
oauth2_issuer_url = ""
oauth2_scopes = ["", ""]

# Multiple accounts supported — add more [[accounts]] blocks
# Switch between them with `ctrl+a` in the inbox

# Root-level settings
store_sent_drafts_in_sending_account = false  # default: Sent/Drafts stay in the first IMAP account

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
# work = "Work"  # optional custom folder; add "work" to tab_order to show as a tab (gb to go, Mb to move -b for business as w was taken)
# tab_order controls the left-to-right tab sequence; omit to use the built-in default order. e.g.:
# tab_order = ["inbox", "to_screen", "feed", "papertrail", "waiting", "someday", "scheduled", "sent", "archive", "screened_out", "drafts", "trash"]
# Gmail uses different folder names — see docs/content/gmail.md for the correct mapping.

[ui]
theme                = "kanagawa"   # kanagawa | kanagawa-paper | kanagawa-light | rose-pine | gruvbox | osaka-jade
inbox_count          = 200      # how many newest emails neomd loads per folder/reload
auto_screen_on_load  = true     # screen inbox automatically on every load (default true)
bg_sync_interval     = 5        # background sync interval in minutes; 0 = disabled (default 5)
bulk_progress_threshold = 10    # show progress counter for batch operations larger than this (default 10)
draft_backup_count      = 20    # rolling compose backups in ~/.cache/neomd/drafts/ (default 20, -1 = disabled)
mark_as_read_after_secs = 7     # seconds in reader before marking as read; 0 = immediate (default 7)
signature   = """**Your Name**
Your Title, Your Company

Connect: [LinkedIn](https://example.com/)

*sent from [neomd](https://neomd.ssp.sh)*"""
```


{{< callout type="info" >}}
**Gmail** uses different IMAP folder names (`[Gmail]/Sent Mail`, `[Gmail]/Trash`, etc.). See [Gmail Configuration](gmail/) for the correct mapping.
{{< /callout >}}

Use an app-specific password (Gmail, Fastmail, Hostpoint, etc.) rather than your main account password.

`inbox_count` is a fetch cap for normal folder loads and startup auto-screening. If you want to re-screen the entire Inbox on the IMAP server, use `:screen-all` from inside neomd; that scans every Inbox email, not just the loaded subset, and can take a while on large mailboxes.



## Passwords: Env and Keyring


### Environment Variables

The `password` and `user` fields support environment variable expansion. If the entire value is a single env var reference, neomd resolves it at startup:

```toml
password = "$IMAP_PASS"        # $VAR form
password = "${IMAP_PASS}"      # ${VAR} form
```

Values containing other text or multiple `$` signs are left as-is, so passwords that happen to contain `$` are never mangled.

Credentials are stored only in `~/.config/neomd/config.toml` (mode 0600) and never written elsewhere; all IMAP connections use TLS (port 993) or STARTTLS (port 143).

### Storing passwords in the OS keyring (Linux)

Set `password = "keyring"` to fetch the password from your OS keyring (macOS Keychain, GNOME Keyring / KDE Wallet via Secret Service on Linux, Windows Credential Manager) at startup. The lookup uses the `[[accounts]].name` as the account identifier under service `neomd`.

```toml
[[accounts]]
name     = "Personal"
password = "keyring"           # resolved at startup; see below for setup
# ...rest of the account
```

**Setup before first launch (Linux, using `secret-tool` from `libsecret`):**

`zalando/go-keyring` writes Secret Service entries with two attributes — `service` (always `neomd`) and `username` (`account/<name>/password`, where `<name>` is the `[[accounts]].name` from your config). The `--label` is display-only; entries are identified by their **attributes**.

```sh
# Add the entry (you'll be prompted for the password on stdin):
secret-tool store --label "neomd Personal" service neomd username account/Personal/password

# Verify it was stored (read-only):
secret-tool lookup service neomd username account/Personal/password

# Audit every neomd entry currently in your keyring:
secret-tool search --all service neomd

# Remove a specific entry if you want to start over:
secret-tool clear service neomd username account/Personal/password
```

> [!IMPORTANT]
> **Multiple accounts → multiple distinct `username` values.** Same `service+username` overwrites, regardless of label. For three accounts named `Personal`, `Work`, `WorkInfo` in your config, run **three** commands with **three different** `username=account/<name>/password` values — labels are not enough.

```sh
secret-tool store --label "neomd Personal"  service neomd username account/Personal/password
secret-tool store --label "neomd Work"      service neomd username account/Work/password
secret-tool store --label "neomd WorkInfo"  service neomd username account/WorkInfo/password
```

> **Recommended: avoid spaces in `name = "..."`** (use `WorkInfo` rather than `"Work Info"`). Easier to type in `secret-tool` commands without quoting; the keyring username is just `account/WorkInfo/password`. If you do use a space, quote the whole username arg every time: `username "account/Work Info/password"`.

`secret-tool` only touches entries that match the **exact** attribute set you pass. It cannot read, modify, or delete other applications' keyring entries — your Firefox / GNOME Online Accounts / SSH Agent / browser passwords stay isolated under their own `service=` namespaces. Worst case if you typo: a stale entry sits unused, removable with `secret-tool clear` above.

If the keyring entry is missing or the keyring service is unavailable, neomd prints a warning and the literal sentinel `"keyring"` is used as the password — IMAP/SMTP authentication will then fail with a clear error. `[[senders]]` aliases that reference an account inherit the resolved keyring password automatically.

OAuth2 tokens are also stored in the keyring under `username = account/<name>/oauth2` with the same `service = neomd`, falling back to `~/.config/neomd/tokens/<account>.json` (mode `0600`) when no keyring is available (headless / SSH systems).

## TLS and STARTTLS Configuration

Neomd automatically determines the correct encryption method based on the port and the optional `starttls` config field:

**IMAP ports:**
- `993` → Implicit TLS (standard IMAPS)
- `143` → STARTTLS upgrade (standard IMAP)
- Non-standard ports (e.g., `1143` for Proton Mail Bridge) → TLS by default
- Set `starttls = true` to force STARTTLS on any port

**SMTP ports:**
- `465` → Implicit TLS (SMTPS)
- `587` → STARTTLS upgrade (modern submission standard)
- Non-standard ports (e.g., `1025` for Proton Mail Bridge) → TLS by default
- Set `starttls = true` to force STARTTLS on any port

**Examples:**

Standard provider (Gmail, Hostpoint, etc.):
```toml
[[accounts]]
imap = "imap.gmail.com:993"
smtp = "smtp.gmail.com:587"
starttls = false  # optional, default behavior works
tls_cert_file = ""
```

Proton Mail Bridge (local bridge on non-standard ports):
```toml
[[accounts]]
imap = "127.0.0.1:1143"  # Uses TLS automatically
smtp = "127.0.0.1:1025"  # Uses TLS; set starttls=true if bridge uses STARTTLS
starttls = false
tls_cert_file = "~/ProtonBridge/cert.pem"  # optional: exported Bridge cert
```

Custom server with STARTTLS on non-standard port:
```toml
[[accounts]]
imap = "mail.custom.com:2143"
smtp = "mail.custom.com:2587"
starttls = true  # Forces STARTTLS instead of TLS
```

See [Proton Bridge Setup](proton-bridge) for complete Proton Mail Bridge setup instructions.

For localhost/self-signed bridges such as Proton Mail Bridge, neomd first tries
normal certificate verification. If that fails with an unknown-authority error
on a loopback host (`127.0.0.1`, `::1`, `localhost`), neomd retries once with a
localhost-only fallback so existing Bridge setups keep working. If you want
strict verification, export the Bridge certificate and set `tls_cert_file`.

## Sent and Drafts Storage

When multiple accounts or `[[senders]]` aliases are configured, SMTP delivery
always uses the selected sending identity's account.

By default, IMAP storage for Sent and Drafts uses the first configured account,
so one primary mailbox owns your sent/draft archive:

```toml
store_sent_drafts_in_sending_account = false
```

If you want Sent and Drafts to follow the selected sending account instead, set:

```toml
store_sent_drafts_in_sending_account = true
```

## Send-Only Accounts (`imap_disabled`)

Set `imap_disabled = true` on an account to use it only for sending. Neomd will skip IMAP connection, folder fetching, and screening for that account. The account remains available as a From address via `ctrl+f` in compose/pre-send.

```toml
[[accounts]]
name          = "Gmail"
imap          = "imap.gmail.com:993"
smtp          = "smtp.gmail.com:587"
user          = "me@gmail.com"
password      = "$GMAIL_APP_PASSWORD"
from          = "Me <me@gmail.com>"
imap_disabled = true
```

`ctrl+a` account cycling skips IMAP-disabled accounts. Useful for adding a provider purely for sending without fetching its emails.

## Sending and Discarding

To abort a compose without sending, close neovim with `ZQ` or `:q!` (discard). To send, save normally with `ZZ` or `:wq`.

## Signature

The `signature` field in `[ui]` is appended automatically when opening a new compose buffer (`c`). It is **not** added for replies. The separator `--` is inserted for you — just write the signature body in Markdown.

Use TOML triple-quoted strings (`"""`) to preserve line breaks. The signature appears at the end of the buffer — you can edit or delete it before saving.

### HTML Signatures

For professional HTML signatures (with logos, tables, styled text), use the `[ui.signature_block]` config with separate `text` and `html` fields:

```toml
[ui.signature_block]
  text = """[html-signature]"""

  html = """<table cellpadding="0" cellspacing="0" border="0" style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; font-size: 14px; line-height: 1.6; color: #333; margin-top: 20px;">
  <tr>
    <td style="padding-right: 20px; vertical-align: top;">
      <img src="https://example.com/logo.png" alt="Company Name" width="80" style="display: block; border: 0;">
    </td>
    <td style="border-left: 2px solid #e0e0e0; padding-left: 20px;">
      <div style="margin-bottom: 8px;">
        <strong style="font-size: 16px; color: #1a1a1a;">Your Name</strong><br>
        <span style="color: #666; font-size: 13px;">Your Title, Company Name</span>
      </div>
      <div style="margin-bottom: 6px; font-size: 13px; color: #888;">
        <span>Connect:</span>
        <a href="https://linkedin.com/in/..." style="text-decoration: none; margin: 0 4px;">LinkedIn</a>
      </div>
      <div style="font-size: 11px; color: #999; font-style: italic;">
        sent from <a href="https://neomd.ssp.sh" style="text-decoration: none;">neomd</a>
      </div>
    </td>
  </tr>
</table>"""
```

**How it works:**

- **Text signature** — appears in the editor buffer and in the `text/plain` MIME part
- **HTML signature** — appends to the `text/html` MIME part only (recipients using HTML email clients see this)
- **`[html-signature]` placeholder** — include this in your text signature to enable HTML signature for a specific email; visible in the editor and pre-send preview, but stripped before sending
- **Per-email control** — delete the `[html-signature]` line in the editor to send without the HTML signature for that email


#### This is how it looks

The sent e-mail with above HTML signature looks like this:
![HTML signature example](/images/html-signature.png)

In the email as text:
```markdown
Hello there

how are you
here's my new HTML signature below.
BR Simon

--
[html-signature]
```


**Notes:**

- Use inline styles only (no `<style>` blocks or external CSS) for maximum email client compatibility
- Host logo images externally (e.g., `https://example.com/logo.png`) so they display for recipients
- The `text` field is backward compatible: if empty, neomd falls back to the legacy `signature` field
- The `--` separator is added automatically before the text signature


## Theming

Pick from six built-in palettes via `[ui].theme`:

| Name | Mode | Source |
|---|---|---|
| `kanagawa` (default) | dark | https://github.com/rebelot/kanagawa.nvim |
| `kanagawa-paper` | dark | https://github.com/thesimonho/kanagawa-paper.nvim |
| `kanagawa-light` | **light** | Lotus palette from kanagawa.nvim, paperwhite (#F2EFE9) background |
| `rose-pine` | dark | https://github.com/rose-pine/rose-pine-theme |
| `gruvbox` | dark | https://github.com/morhetz/gruvbox |
| `osaka-jade` | dark | https://github.com/Justikun/omarchy-osaka-jade-theme |

Override individual colour slots (any subset) via the optional `[theme]` block:

```toml
[ui]
theme = "rose-pine"

[theme]
# All slots are hex strings ("#rrggbb"). Empty / missing slots fall back
# to the named built-in.
primary       = "#ff79c6"   # accent (active tab, header, autocomplete)
unread        = "#bd93f9"   # unread email emphasis
error         = "#ff5555"
# bg, border, subtle, selected, text, muted, number, date,
# author_read, subject_read, size_col, author_unread, subject_unread, success
```

## Calendar invites

```toml
[calendar]
open_command = "xdg-open"   # what `<space> v o` runs to import .ics into your local calendar app
                            # Linux: defaults to xdg-mime registration; set to "morgen", "khal",
                            # "/usr/bin/gnome-calendar", etc. to force a specific app
```

Workflow + caveats (sending an iMIP REPLY ≠ importing into your calendar) are documented in [Reading → Calendar Invites](../reading/#calendar-invites-icalendar--rsvp).

## AI handoff (pre-send `i` key)

`[ai]` wires any external CLI to the pre-send `i` key. neomd:

1. Shows a one-line prompt for your instruction (e.g. `fix grammar`, `make it more formal`, `tighten this`).
2. Writes the current draft to a temp markdown file (with the same `# [neomd: ...]` headers used during compose).
3. Spawns the command with the file path appended as the last arg. Any `{prompt}` token in `args` is replaced by what you typed.
4. Re-reads the file on exit so the AI's edits replace your draft body.

Press Enter on an empty prompt to run interactively (no `{prompt}` substitution); type an instruction + Enter to run non-interactively; press Esc to cancel. Quit the AI tool (`ctrl+c`, `q`, `/quit`, `ZZ`, …) to return to neomd's pre-send screen.

```toml
[ai]
command = "claude"                      # default: Claude Code CLI
args    = ["edit {file}: {prompt}"]     # default: tells claude what file + what to do
# command = "codex"
# command = "aichat"
```

Two placeholders are substituted at spawn time: `{prompt}` becomes your typed instruction, `{file}` becomes the draft's basename. neomd also sets the spawned process's working directory to the temp dir holding the draft, so claude's built-in Edit tool reaches the file natively (no `--add-dir` needed).

If you type `fix grammar` at the prompt, the spawn is `claude "edit neomd-ai-XYZ.md: fix grammar"` running in `/tmp/neomd/`. Claude opens interactively, sees the file in cwd, edits in place, you `/quit` when satisfied, neomd picks up the changes.

> [!IMPORTANT]
> Default args use the **interactive** form, not `claude -p`. The `-p` (print) flag in Claude Code is non-interactive and bills against your **API credits** rather than your Claude Pro/Max subscription — it leaks money even when you're paying for a plan. Interactive mode runs under your subscription auth. Only switch to `args = ["-p", "edit {file}: {prompt}"]` if you have an API key with credits and explicitly want the scripted, no-review flow.

If you press Enter on an empty prompt, only the `{prompt}` placeholder is replaced (with `""`) — the resulting `"edit neomd-ai-XYZ.md: "` still tells claude which file to look at, so claude opens interactively in the temp dir with that file pre-mentioned and waits for your follow-up instruction.

`nvim` is intentionally **not** the default: the compose buffer is already open in nvim before pre-send, so spawning nvim on `i` would just re-edit. You can already use [avante.nvim](https://github.com/yetone/avante.nvim) or others within neovim composer to do any AI you'd like. 
But instead, pick a tool that does work. The handoff reuses the same parser as the regular editor flow, so headers (To, Cc, Bcc, Subject) the AI tool may rewrite are picked up automatically. If `command` is empty the `i` key is a no-op.

## OAuth2 Authentication

Neomd supports OpenAuth2 authenticated accounts, you just need to add `oauth2_client_id`, `oauth2_client_secret`, `oauth2_scopes` and `oauth2_issuer_url`.

Note that when using oauth2 authentication, the password field is not required in the account configuration.

### Issuer URL

By default, if an issuer URL is provided, i.e.: `https://login.microsoftonline.com/common/v2.0` for Office265 accounts, neomd will search for the OpenID Connect discovery URL: `/.well-known/openid-configuration` resolving then the `oauth2_token_url` and `oauth2_auth_url`. These parameters can be provided manually as well.

### Scopes

The scopes required depends on the provider and is better confirmed by your email provider. As an example, for Office365 acounts, the following scopes are required for IMAP: `"https://outlook.office365.com/IMAP.AccessAsUser.All", "offline_access"`.

### Reference documentation for GMAIL and Office365

- To enable OAuth2 authentication for Office365 accounts, follow the documentation [here](https://learn.microsoft.com/en-us/exchange/client-developer/legacy-protocols/how-to-authenticate-an-imap-pop-smtp-application-by-using-oauth)
- For GMAIL, follow the documentation [here](https://developers.google.com/workspace/gmail/imap/xoauth2-protocol)


## Dedicated Platforms


- [Gmail Configuration](gmail/)
- [Proton Mail Bridge](proton-bridge/)
- [Android (Termux)](android/)
- [Headless Daemon Mode](headless/)
- [Email Standards](email-standards/)
