# Plan: neomd — Minimal Neovim-flavored Markdown Email Client

## Context

The user wants a small, beautiful terminal email client that feels like neomutt but is built from scratch with a simpler codebase. Key motivations:
- Write and read emails in Markdown (composed in neovim, rendered with glamour)
- HEY-style screener: folder/tag-based inbox gating (allowlist already exists)
- Send as multipart/alternative (plain text + minimal HTML) so links and formatting render nicely for recipients
- Charmbracelet aesthetic (bubbletea TUI, glamour, lipgloss)
- Go (preferred, already used in msgvault and hey-cli)

MVP scope: **Inbox list → Read email → Compose in neovim → Send via SMTP**

---

## Architecture

```
neomd/
├── cmd/neomd/
│   └── main.go               # entry point: load config, start bubbletea
├── internal/
│   ├── config/
│   │   └── config.go         # TOML reader → ~/.config/neomd/config.toml
│   ├── imap/
│   │   └── client.go         # go-imap/v2: connect, list folders, fetch, move
│   ├── smtp/
│   │   └── sender.go         # net/smtp TLS: build multipart/alt MIME, send
│   ├── screener/
│   │   ├── screener.go       # load/save allowlists; classify incoming email
│   │   └── lists.go          # read screened_in.txt, screened_out.txt, feed.txt, papertrail.txt
│   ├── editor/
│   │   └── editor.go         # spawn $EDITOR (nvim), return tmp file content
│   ├── render/
│   │   ├── markdown.go       # glamour: markdown → ANSI for viewport
│   │   └── html.go           # goldmark: markdown → HTML (for sending)
│   └── ui/
│       ├── model.go          # root bubbletea Model, viewState enum, Update, View
│       ├── inbox.go          # bubbles/list for inbox, folder switcher
│       ├── reader.go         # bubbles/viewport for reading email
│       ├── compose.go        # bubbles/textinput (To, Subject), then nvim
│       └── styles.go         # lipgloss palette and layout
├── go.mod
└── go.sum
```

---

## Key Dependencies

```
github.com/charmbracelet/bubbletea   v1.3.x   # TUI (same as msgvault)
github.com/charmbracelet/bubbles     v1.x      # list, viewport, textinput, spinner
github.com/charmbracelet/glamour     v0.x      # markdown → ANSI rendering
github.com/charmbracelet/lipgloss    v1.x      # styling
github.com/emersion/go-imap/v2       v2.x      # IMAP (already proven in msgvault)
github.com/emersion/go-message       v0.18.x   # MIME/header parsing
github.com/yuin/goldmark             v1.x      # Markdown → HTML for sending
github.com/BurntSushi/toml           v1.x      # config (same as msgvault)
```

---

## Config File

`~/.config/neomd/config.toml` (auto-created with placeholder on first run):

```toml
[account]
name     = "Personal"
imap     = "imap.example.com:993"   # TLS; :143 + starttls = true for STARTTLS
smtp     = "smtp.example.com:587"
user     = "me@example.com"
password = "app-password"
from     = "Me <me@example.com>"

[screener]
# paths to existing allowlist files (reuse from neomutt setup)
screened_in   = "~/.config/mutt/screened_in.txt"
screened_out  = "~/.config/mutt/screened_out.txt"
feed          = "~/.config/mutt/feed.txt"
papertrail    = "~/.config/mutt/papertrail.txt"

[folders]
inbox      = "INBOX"
sent       = "Sent"
trash      = "Trash"
drafts     = "Drafts"
to_screen  = "ToScreen"
feed       = "Feed"
papertrail = "PaperTrail"
screened_out = "ScreenedOut"

[ui]
theme       = "dark"   # dark | light | auto
inbox_count = 50
```

---

## TUI State Machine

```
viewState enum:
  stateInbox    → bubbles/list of email summaries
  stateReading  → bubbles/viewport with glamour-rendered body
  stateCompose  → textinput for To/Subject, then hands off to $EDITOR
  stateToScreen → list of unscreened senders awaiting decision

Transitions:
  Inbox      →[Enter]→  Reading
  Inbox      →[c]→      Compose
  Inbox      →[Tab]→    cycle folders (Inbox / ToScreen / Feed / PaperTrail)
  ToScreen   →[I]→      approve sender → add to screened_in.txt, move to INBOX
  ToScreen   →[O]→      block sender  → add to screened_out.txt, move to ScreenedOut
  ToScreen   →[F]→      mark as Feed  → add to feed.txt, move to Feed
  ToScreen   →[P]→      mark PaperTrail → add to papertrail.txt, move to PaperTrail
  Reading    →[q]→      Inbox
  Compose    →[Enter after Subject]→ suspend TUI → nvim → resume → send → Inbox
```

---

## IMAP Flow (internal/imap/client.go)

Adapted from `/home/sspaeti/git/email/msgvault/internal/imap/client.go`:

- `Connect()` → `imapclient.DialTLS` or `DialStartTLS`
- `FetchHeaders(folder string, n int) []EmailSummary` → SELECT folder, UID FETCH last N with ENVELOPE
- `FetchBody(uid uint32) string` → UID FETCH BODY[], parse with go-message:
  - Prefer `text/plain` part
  - Fall back to stripping `text/html` if no plain part
- `MoveMessage(uid, from, to string)` → UID COPY + UID STORE \Deleted + EXPUNGE

Async pattern: bubbletea `tea.Cmd` functions emit typed messages (`inboxLoadedMsg`, `bodyLoadedMsg`, `errMsg`).

**Offline support (future):** Architecture leaves room to swap the IMAP client for a Maildir reader (mbsync-synced local Maildir). The `imap.Client` interface can be backed by either live IMAP or local Maildir — same interface, swap implementation. For now: live IMAP only.

---

## Screener (internal/screener/)

Reuses the four existing plain-text lists from the neomutt setup:
```
screened_in.txt    — approved senders (one email per line)
screened_out.txt   — blocked senders
feed.txt           — newsletter/feed senders
papertrail.txt     — receipt/notification senders
```

```go
type Screener struct {
    screenedIn  []string  // loaded at startup
    screenedOut []string
    feed        []string
    papertrail  []string
}

func (s *Screener) Classify(from string) Category
// Category: Inbox | ToScreen | ScreenedOut | Feed | PaperTrail

func (s *Screener) Approve(email string) error   // append to screened_in.txt
func (s *Screener) Block(email string) error      // append to screened_out.txt
func (s *Screener) MarkFeed(email string) error   // append to feed.txt
func (s *Screener) MarkPaperTrail(email string)   // append to papertrail.txt
```

On startup, neomd can optionally run a screening pass on INBOX: any unrecognized sender is moved to `ToScreen` (same logic as `initial_screening.sh`).

---

## Sending: Multipart/Alternative (plain text + HTML)

**The problem with plain text only:** markdown syntax like `[link](url)` shows as literal text. Links are unclickable. Bold `**text**` shows with asterisks.

**Solution:** Send as `multipart/alternative` — every mail client picks the best part:
- `text/plain` — the raw markdown as typed (readable, no rendering needed)
- `text/html` — goldmark-converted HTML wrapped in a minimal CSS template

```go
// internal/render/html.go
func MarkdownToHTML(md string) (string, error) {
    // Use goldmark to convert markdown → HTML fragment
    // Wrap in minimal template (derived from listmonk template):
    //   max-width 650px, system fonts, styled links, <pre> for code
    //   No tracking pixels, no complex layout
}

// internal/smtp/sender.go
func Send(cfg Config, to, subject, markdownBody string) error {
    plainText := markdownBody                  // raw markdown = readable plain text
    htmlBody, _ := render.MarkdownToHTML(markdownBody)

    // Build multipart/alternative MIME message
    // Part 1: text/plain; charset=utf-8
    // Part 2: text/html; charset=utf-8
    // Headers: From, To, Subject, Date, Message-ID
    // Send via net/smtp with STARTTLS
}
```

**Minimal HTML wrapper** (inlined from listmonk template, stripped to essentials):
```html
<html><body style="font-family:system-ui,sans-serif;max-width:650px;
  margin:0 auto;padding:20px;color:#333;line-height:1.6">
  {{ BODY }}
</body></html>
```

This gives recipients proper link rendering, bold/italic, code blocks — while the sender still writes pure markdown in neovim.

Reference template: `/home/sspaeti/git/sspaeti.com/listmonk/misc/email-template.html`
Pandoc template (for design reference): `/home/sspaeti/git/general/dotfiles/mutt/.config/mutt/templates/email.html`

---

## Editor Flow (internal/editor/editor.go)

```go
func Compose(prelude string) (string, error) {
    // prelude = "To: ...\nSubject: ...\n\n---\n\n" for context
    f, _ := os.CreateTemp("", "neomd-*.md")
    f.WriteString(prelude)
    f.Close()

    editor := os.Getenv("EDITOR")
    if editor == "" { editor = "nvim" }

    cmd := exec.Command(editor, f.Name())
    cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
    cmd.Run()

    content, _ := os.ReadFile(f.Name())
    os.Remove(f.Name())
    return string(content), nil
}
```

The bubbletea program calls `tea.Suspend` before spawning nvim, then `tea.Resume` after — same pattern hey-cli uses for external processes.

---

## Inbox View (ui/inbox.go)

- `bubbles/list` with custom `ItemDelegate`
- Each row: `● From  │  Subject  │  Date` (● = unread indicator)
- Tab key cycles folders: `Inbox` → `ToScreen` → `Feed` → `PaperTrail`
- Folder name shown in header via lipgloss
- Spinner while fetching via `bubbles/spinner`

## Reader View (ui/reader.go)

- `bubbles/viewport` with glamour-rendered body
- Lipgloss bordered header block: From / To / Subject / Date
- `j/k/Space/PgDn` scroll, `q` back to inbox

## Compose View (ui/compose.go)

- Two `bubbles/textinput` fields: **To** and **Subject**
- Tab moves between fields; Enter on Subject → suspend → nvim → resume → send
- Status message in inbox after send

---

## Files to Create (all new in /home/sspaeti/git/email/neomd/)

```
go.mod
go.sum (after go mod tidy)
cmd/neomd/main.go
internal/config/config.go
internal/imap/client.go          ← adapt from msgvault
internal/smtp/sender.go
internal/screener/screener.go
internal/screener/lists.go
internal/editor/editor.go
internal/render/markdown.go
internal/render/html.go
internal/ui/model.go
internal/ui/inbox.go
internal/ui/reader.go
internal/ui/compose.go
internal/ui/styles.go
```

---

## Reference Files

| Purpose | File |
|---------|------|
| IMAP client pattern | `/home/sspaeti/git/email/msgvault/internal/imap/client.go` |
| TUI state machine | `/home/sspaeti/git/email/hey-cli/internal/tui/tui.go` |
| Config parsing | `/home/sspaeti/git/email/msgvault/internal/config/config.go` |
| Screener lists (reuse) | `/home/sspaeti/git/general/dotfiles/mutt/.config/mutt/screened_in.txt` etc. |
| Screener bash logic | `/home/sspaeti/git/general/dotfiles/mutt/.config/mutt/initial_screening.sh` |
| HTML email template | `/home/sspaeti/git/sspaeti.com/listmonk/misc/email-template.html` |
| Pandoc email template | `/home/sspaeti/git/general/dotfiles/mutt/.config/mutt/templates/email.html` |
| SMTP config reference | `/home/sspaeti/git/general/dotfiles/mutt/.msmtprc` |
| Neomutt C source | `/home/sspaeti/git/email/neomutt/` (reference for edge cases: imap/, notmuch/) |

---

## Offline Support (Future, not MVP)

Architecture is designed for this. The IMAP layer will expose an interface:

```go
type MailStore interface {
    FetchHeaders(folder string, n int) ([]EmailSummary, error)
    FetchBody(folder string, uid uint32) (string, error)
    MoveMessage(uid uint32, from, to string) error
}
```

MVP: `ImapStore` (live connection via go-imap/v2).
Future: `MaildirStore` (local sync via mbsync → reads Maildir directly, no network needed).

---

## Verification

1. `go build ./cmd/neomd` — compiles cleanly
2. Fill `~/.config/neomd/config.toml` with real IMAP/SMTP credentials
3. `./neomd` → inbox loads, emails listed with sender/subject/date
4. Tab → switch to ToScreen folder; press `I` on an email → sender added to screened_in.txt
5. Enter on inbox email → glamour-rendered body in viewport
6. Press `c` → fill To/Subject → nvim opens `neomd-*.md` → write markdown → save → email sent
7. Recipient receives email with properly rendered HTML (links clickable, bold/italic work) and plain text fallback
