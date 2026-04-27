# Security

neomd handles IMAP/SMTP credentials and email content. This document explains what is stored, where, and how it is protected. Links to the relevant source files are included so you can verify the implementation directly.

---

## Credentials

| What | Where | Permission |
|------|-------|-----------|
| IMAP/SMTP password, username | `~/.config/neomd/config.toml` | `0600` (user-readable only) |
| Config directory | `~/.config/neomd/` | `0700` |

- The config file is **created once** at `0600` and **never written back** after that — neomd only reads it at startup.
- Passwords can be stored as plain strings or as environment variable references (`$MY_PASS` / `${MY_PASS}`), so they never need to appear in the file at all.
- Credentials **never appear** in error messages, the status bar, or any log output.

**Code:** [`internal/config/config.go`](https://github.com/ssp-data/neomd/blob/main/internal/config/config.go) — `Load()`, `writeDefault()`, `expandEnv()`

---

## Network connections

| Protocol | Port | How |
|----------|------|-----|
| IMAP | 993 | `imapclient.DialTLS` — TLS enforced |
| IMAP | 143 | `imapclient.DialStartTLS` — STARTTLS negotiated |
| Any other port | — | **Refused** — neomd errors out rather than connect unencrypted |
| SMTP | 465 | Explicit `tls.Dial` before any auth |
| SMTP | 587 | Go stdlib `PlainAuth` guarantee — refuses credentials over non-TLS (except localhost); note: this is a stdlib property, not enforced by neomd code |

**Code:** [`internal/imap/client.go`](https://github.com/ssp-data/neomd/blob/main/internal/imap/client.go) — `connect()` · [`internal/smtp/sender.go`](https://github.com/ssp-data/neomd/blob/main/internal/smtp/sender.go) — `Send()`

---

## Screener lists (sensitive email addresses)

The five screener lists — `screened_in.txt`, `screened_out.txt`, `feed.txt`, `papertrail.txt`, `spam.txt` — contain sender email addresses you have explicitly classified. These are **stored outside neomd**, at paths you configure:

```toml
[screener]
screened_in  = "~/.dotfiles/neomd/.lists/screened_in.txt"
screened_out = "~/.dotfiles/neomd/.lists/screened_out.txt"
feed         = "~/.dotfiles/neomd/.lists/feed.txt"
papertrail   = "~/.dotfiles/neomd/.lists/papertrail.txt"
spam         = "~/.dotfiles/neomd/.lists/spam.txt"
```

neomd never chooses these paths — you control them. When neomd appends or rewrites a list file, it always uses mode `0600`. This means:
- The lists can live alongside an existing neomutt/mutt setup and be shared with it.
- They are under your own dotfiles/version control — or not — entirely your choice.
- neomd has no server, no sync, and no telemetry; the files never leave your machine.

**Code:** [`internal/screener/screener.go`](https://github.com/ssp-data/neomd/blob/main/internal/screener/screener.go) — `appendLine()`, `removeFromList()`

---

## Temporary files

When composing, reading in w3m, or opening in a browser, neomd writes a temporary file via `os.CreateTemp` (default mode `0600`) and registers a `defer os.Remove` so it is deleted immediately after use. The compose temp file (`neomd-*.md`) is deleted whether you send or abort.

**Code:** [`internal/ui/model.go`](https://github.com/ssp-data/neomd/blob/main/internal/ui/model.go) — `openInBrowser()`, `openInW3m()`, `openInEditor()` · [`internal/editor/editor.go`](https://github.com/ssp-data/neomd/blob/main/internal/editor/editor.go) — `Compose()`

---

## Command history

The `:` command history is written to `~/.cache/neomd/cmd_history` (cache dir, `0600`). It stores **command names only** (e.g. `screen`, `reload`) — never email addresses, subjects, or credentials. The cache directory is intentionally outside `~/.config` so it is never picked up by dotfile version control.

**Code:** [`internal/config/config.go`](https://github.com/ssp-data/neomd/blob/main/internal/config/config.go) — `HistoryPath()`

---

## URL handling

All email-extracted URLs — both numbered inline links (`space+digit`) and `ctrl+o` / `List-Post` web version links — are validated before being passed to the browser. Only `http://`, `https://`, and `mailto:` schemes are allowed. URLs with any other scheme (e.g. `javascript:`, `data:`) are blocked and shown as an error in the status bar.

**Code:** [`internal/ui/model.go`](https://github.com/ssp-data/neomd/blob/main/internal/ui/model.go) — `openLinkCmd()`, `openWebVersion()`

---

## Spy pixel blocking

neomd automatically detects and blocks tracking pixels (1x1 invisible images embedded by newsletter services like Mailchimp, HubSpot, and SendGrid to track email opens).

**How it works:**
- The TUI renders emails as styled Markdown via glamour — **no HTTP requests** are made during rendering, so tracking servers are never contacted. Senders cannot tell if you read their email.
- `cleanMarkdown()` detects empty-alt-text images (the signature of tracking pixels), counts them, extracts the tracker domains, and strips them from the rendered output.
- The inbox list shows a `⊙` indicator (orange) for emails that contained tracking pixels, visible after first read.
- The reader header shows `⊙ N spy pixel(s) blocked (domain.com, ...)` with the tracker domains.

**Browser view (`O`) protection:**
- The HTML template includes a `Content-Security-Policy` meta tag that restricts image sources to `file:`, `data:`, and `cid:` only. Remote images (including tracking pixels) are blocked even when viewing the full HTML version in a browser.

**Code:** [`internal/imap/client.go`](https://github.com/ssp-data/neomd/blob/main/internal/imap/client.go) — `cleanMarkdown()`, `SpyPixelInfo` · [`internal/render/html.go`](https://github.com/ssp-data/neomd/blob/main/internal/render/html.go) — `htmlTemplate` (CSP) · [`internal/ui/inbox.go`](https://github.com/ssp-data/neomd/blob/main/internal/ui/inbox.go) — `⊙` indicator · [`internal/ui/reader.go`](https://github.com/ssp-data/neomd/blob/main/internal/ui/reader.go) — `renderEmailHeader()`

---

## Attachment safety

Attachments are saved to `~/Downloads/` and opened with `xdg-open`. To prevent accidental execution of malicious files, neomd maintains a blocklist of dangerous file extensions (`.sh`, `.exe`, `.desktop`, `.bat`, `.py`, `.jar`, etc.). Files with these extensions are **saved but not auto-opened** — the status bar warns that the file type is dangerous and the user must open it manually.

**Code:** [`internal/ui/model.go`](https://github.com/ssp-data/neomd/blob/main/internal/ui/model.go) — `dangerousExts`, `downloadOpenAttachmentCmd()`

---

## Reporting a vulnerability

Open a [GitHub issue](https://github.com/ssp-data/neomd/issues) or email the maintainer directly (address in the commit history). neomd is a personal tool with no release SLA, but security reports are taken seriously and addressed promptly.

## Disclaimer
This security audit is instructed by me, checking all relevant folders, but executed by Claude Code.
