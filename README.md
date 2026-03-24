# neomd

A minimal terminal email client for people who write in Markdown and live in Neovim.

Compose emails in your editor, read them rendered with [glamour](https://github.com/charmbracelet/glamour), and manage your inbox with a [HEY-style screener](https://www.hey.com/features/the-screener/) — all from the terminal.

## Features

- **Write in Markdown, send beautifully** — compose in `$EDITOR` (defaults to `nvim`), send as `multipart/alternative`: raw Markdown as plain text + goldmark-rendered HTML so recipients get clickable links and formatting
- **Glamour reading** — incoming emails rendered as styled Markdown in the terminal
- **HEY-style screener** — unknown senders land in `ToScreen`; press `I/O/F/P` to approve, block, mark as Feed, or mark as PaperTrail; reuses your existing `screened_in.txt` lists from neomutt
- **Folder tabs** — switch between Inbox, ToScreen, Feed, and PaperTrail with `Tab`
- **IMAP + SMTP** — direct connection, no local sync daemon required

## Install

```sh
git clone https://github.com/sspaeti/neomd
cd neomd
make install   # installs to ~/.local/bin/neomd
```

Or just build locally:

```sh
make build
./neomd
```

## Configuration

On first run, neomd creates `~/.config/neomd/config.toml` with placeholders:

```toml
[account]
name     = "Personal"
imap     = "imap.example.com:993"   # :993 = TLS, :143 = STARTTLS
smtp     = "smtp.example.com:587"
user     = "me@example.com"
password = "app-password"
from     = "Me <me@example.com>"

[screener]
# reuse your existing neomutt allowlist files
screened_in  = "~/.config/mutt/screened_in.txt"
screened_out = "~/.config/mutt/screened_out.txt"
feed         = "~/.config/mutt/feed.txt"
papertrail   = "~/.config/mutt/papertrail.txt"

[folders]
inbox       = "INBOX"
sent        = "Sent"
trash       = "Trash"
to_screen   = "ToScreen"
feed        = "Feed"
papertrail  = "PaperTrail"
screened_out = "ScreenedOut"

[ui]
theme       = "dark"   # dark | light | auto
inbox_count = 50
```

Use an app-specific password (Gmail, Fastmail, etc.) rather than your main account password.

## Keybindings

### Inbox

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate up/down |
| `Enter` | Open email |
| `c` | Compose new email |
| `Tab` | Switch folder (Inbox → ToScreen → Feed → PaperTrail) |
| `r` | Refresh current folder |
| `/` | Filter emails |
| `q` | Quit |

### ToScreen folder

| Key | Action |
|-----|--------|
| `I` | Approve sender → move to Inbox, add to `screened_in.txt` |
| `O` | Block sender → move to ScreenedOut, add to `screened_out.txt` |
| `F` | Mark as Feed → move to Feed folder, add to `feed.txt` |
| `P` | Mark as PaperTrail → move to PaperTrail, add to `papertrail.txt` |

### Reading

| Key | Action |
|-----|--------|
| `j` / `k` / `Space` | Scroll |
| `q` / `Esc` | Back to inbox |

### Composing

| Key | Action |
|-----|--------|
| `Tab` / `Enter` | Move to next field |
| `Enter` (on Subject) | Open `$EDITOR` with a `.md` temp file |
| `Esc` | Cancel |

After saving and closing the editor, the email is sent automatically.

## How Sending Works

neomd sends every email as `multipart/alternative`:

- **`text/plain`** — the raw Markdown you wrote (readable as-is in any client)
- **`text/html`** — rendered by [goldmark](https://github.com/yuin/goldmark) with a clean CSS wrapper

This means recipients using Gmail, Apple Mail, Outlook, etc. see properly formatted links, bold, headers, and code blocks — while you write nothing but Markdown.

## Make Targets

```
make build    compile ./neomd
make run      build and run
make install  install to ~/.local/bin
make test     run tests
make vet      go vet
make fmt      gofmt -w .
make tidy     go mod tidy
make clean    remove compiled binary
make help     print this list
```

## Stack

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — TUI framework
- [Bubbles](https://github.com/charmbracelet/bubbles) — list, viewport, textinput components
- [Glamour](https://github.com/charmbracelet/glamour) — Markdown → terminal rendering
- [Lipgloss](https://github.com/charmbracelet/lipgloss) — styling
- [go-imap/v2](https://github.com/emersion/go-imap) — IMAP client
- [go-message](https://github.com/emersion/go-message) — MIME parsing
- [goldmark](https://github.com/yuin/goldmark) — Markdown → HTML for sending
