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
- `detectSpyPixels()` scans raw HTML for `<img>` tags with empty alt AND at least one of: tiny dimensions (width/height 0–1), CSS hiding, or known tracker URL patterns. This runs before markdown conversion so size/style info is preserved.
- The inbox list shows a `°` indicator (orange) for emails that contained tracking pixels, visible after first read or after running `<space>S` / `:scan-spy-pixels`.
- The reader header shows `° N spy pixel(s) blocked (domain.com, ...)` with the tracker domains.
- Scan results are cached in `~/.cache/neomd/spy_pixels` and persist across restarts. Both positive (has tracker) and negative (scanned clean) results are cached so repeat scans are instant.

**Browser view (`O`):** When you open an email in the browser, a Content-Security-Policy is injected that blocks JavaScript, iframes, and embedded objects (`script-src 'none'; frame-src 'none'; object-src 'none'`). Remote images are intentionally allowed — you're choosing to see the full email. This prevents script execution while preserving the visual experience.

**Code:** [`internal/imap/client.go`](https://github.com/ssp-data/neomd/blob/main/internal/imap/client.go) — `detectSpyPixels()`, `ScanSpyPixels()` · [`internal/render/html.go`](https://github.com/ssp-data/neomd/blob/main/internal/render/html.go) — `SanitizeForBrowser()` · [`internal/ui/inbox.go`](https://github.com/ssp-data/neomd/blob/main/internal/ui/inbox.go) — `°` indicator · [`internal/ui/reader.go`](https://github.com/ssp-data/neomd/blob/main/internal/ui/reader.go) — `renderEmailHeader()`

---

## Attachment safety

Attachments are saved to `~/Downloads/` and opened with `xdg-open`. Two layers of protection prevent accidental execution of malicious files:

1. **Extension blocklist** — files with dangerous extensions (`.sh`, `.exe`, `.desktop`, `.bat`, `.py`, `.jar`, etc.) are saved but **not auto-opened**. The status bar warns about the dangerous file type.

2. **Magic-byte verification** — before opening, neomd inspects the actual file content using Go's `net/http.DetectContentType()` (WHATWG MIME sniffing, first 512 bytes) and compares it against what the file extension claims. If there's a mismatch — e.g. a shell script disguised as `photo.png` (detected as `text/plain`, expected `image/`) — the file is saved but **not auto-opened**. This catches attackers who rename executable files to look like images, PDFs, or other safe types.

| Scenario | Extension | Magic bytes | Result |
|---|---|---|---|
| `malware.sh` | `.sh` → blocked | — | Saved, not opened |
| `malware.sh` → `photo.png` | `.png` → safe | `text/plain` ≠ `image/` | Saved, not opened |
| Real `photo.png` | `.png` → safe | `image/png` ✓ | Opened normally |

**Code:** [`internal/ui/model.go`](https://github.com/ssp-data/neomd/blob/main/internal/ui/model.go) — `dangerousExts`, `isMimeMismatch()`, `downloadOpenAttachmentCmd()`

---

## Screener as a security layer

The [HEY-style screener](https://ssp-data.github.io/neomd/docs/screener/) is primarily a productivity workflow, but it doubles as a phishing defense. Unknown senders never reach your Inbox — they land in `ToScreen` first, where you decide whether to approve them.

This matters because **an email in ToScreen from a sender you already screened in is immediately suspicious**. If you've approved `info@sbb.ch` (Swiss train service), but a new email from `info@sbb-tickets.fake.com` arrives in ToScreen, you know it's an impersonation attempt before you even open it. Without the screener, that phishing email would sit alongside legitimate SBB emails in your Inbox with no visual distinction.

In practice: everything in your Inbox is from senders you've explicitly trusted. ToScreen is your quarantine — treat it with suspicion by default, verify the sender address, and press `$` to mark spam.

**Code:** [`internal/screener/screener.go`](https://github.com/ssp-data/neomd/blob/main/internal/screener/screener.go)

---

## Reporting a vulnerability

Open a [GitHub issue](https://github.com/ssp-data/neomd/issues) or email the maintainer directly (address in the commit history). neomd is a personal tool with no release SLA, but security reports are taken seriously and addressed promptly.

## Disclaimer
This security audit is instructed by me, checking all relevant folders, but executed by Claude Code.
