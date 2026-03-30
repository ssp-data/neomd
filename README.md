# neomd

A minimal terminal email client for people who write in Markdown and live in Neovim.

[Neomd](https://www.ssp.sh/brain/neomd/) is my way of implementing an email TUI based on my experience with Neomutt, focusing on [Neovim](https://www.ssp.sh/brain/neovim) (input) and reading/writing in [Markdown](https://www.ssp.sh/brain/markdown) and navigating with [Vim Motions](https://www.ssp.sh/brain/vim-language-and-motions) with the GTD workflow and [HEY-Screener](https://www.hey.com/features/the-screener/).

## The philosophy behind Neomd: What's unique?

The key here is **speed** in which you can **navigate, read, and process** your email. Everything is just a shortcut away, and instantly (ms not seconds). It's similar to the foundations that Superhuman was built on: it runs on Gmail and makes it fast with vim commands.

With the **HEY-Screener**, you get only emails in your inbox that you _screened in_, no spam or sales pitch before you added them. Or don't like them, just screen them out, and they get automatically moved to the "ScreenedOut" folder.

With the [GTD approach](https://www.ssp.sh/brain/getting-things-done-gtd), using folders such as next (inbox), waiting, someday, scheduled, or archive, you can move them with one shortcut. This allows you quickly to move emails you need to wait for, or deal with later, in the right category. **Processing your email only once**.

With the additional **Feed** and **Papertrail**, two additional features from HEY, you can read newsletters (just hit F) on them automatically in their separate tab, or move all your receipts into the Papertrail. Once you mark them as feed or papertrail, they will moved there automatically going forward. So you decide whether to read emails or news by jumping to different tabs.
## Screenshots

### Overview

Feed view with all Newsletters - also workflow with differnt tabs and unread counter only for certain tabs (not all):
![neomd](images/overview-email-feed.png)

### Reading Panel

Reading an email with Markdown 💙:

![neomd](images/reading-email.png)

### Sent emails

![neomd](images/sent-email.png)

Or in Gmail:
![neomd](images/gmail.png)

This is the markdown sent:

```markdown
# [neomd: to: email@domain.com]
# [neomd: subject: this is an email from neomd!]

This email is from Neomd. Great I can add links such as [this](https://ssp.sh) with plain Markdown.

E.g. **bold** or _italic_.

## Does headers work too?

this is a text before a h3.

### H3 header

how does that look in an email?
Best regards
```

Compose emails in your editor, read them rendered with [glamour](https://github.com/charmbracelet/glamour), and manage your inbox with a [HEY-style screener](https://www.hey.com/features/the-screener/) — all from the terminal.

## Features

- **Write in Markdown, send beautifully** — compose in `$EDITOR` (defaults to `nvim`), send as `multipart/alternative`: raw Markdown as plain text + goldmark-rendered HTML so recipients get clickable links, bold, headers, inline code, and code blocks
- **Pre-send review** — after closing the editor, review To/Subject/body before sending; attach files, save to Drafts, or re-open the editor — no accidental sends
- **Attachments** — attach files from the pre-send screen via yazi (`a`); images appear inline in the email body, other files as attachments; also attach from within neovim via `<leader>a`; the reader lists all attachments (including inline images) and `1`–`9` downloads and opens them
- **CC, BCC, Reply-all** — optional Cc/Bcc fields (toggle with `ctrl+b`); `R` in the reader replies to sender + all CC recipients
- **Drafts** — `d` in pre-send saves to Drafts (IMAP APPEND); `E` in the reader re-opens a draft as an editable compose
- **Multiple From addresses** — define SMTP-only `[[senders]]` aliases (e.g. `s@ssp.sh` through an existing account); cycle with `ctrl+f` in compose and pre-send; sent copies always land in the Sent folder
- **Undo** — `u` reverses the last move or delete (`x`, `A`, `M*`) using the UIDPLUS destination UID
- **Glamour reading** — incoming emails rendered as styled Markdown in the terminal
- **HEY-style screener** — unknown senders land in `ToScreen`; press `I/O/F/P` to approve, block, mark as Feed, or mark as PaperTrail; reuses your existing `screened_in.txt` lists from neomutt
- **Folder tabs** — Inbox, ToScreen, Feed, PaperTrail, Archive, Waiting, Someday, Scheduled, Sent, Trash, ScreenedOut
- **Multi-select** — `m` marks emails, then batch-delete, move, or screen them all at once
- **Auto-screen on load** — screener runs automatically every time the Inbox loads (startup, `R`); keeps your inbox clean without pressing `S` (configurable, on by default)
- **Background sync** — while neomd is open, inbox is fetched and screened every 5 minutes in the background; interval configurable, set to `0` to disable
- **Kanagawa theme** — colors from the [kanagawa.nvim](https://github.com/rebelot/kanagawa.nvim) palette
- **IMAP + SMTP** — direct connection via RFC 6851 MOVE, no local sync daemon required and keeps it in sync if you use it on mobile or different device

## Install

**Prerequisites:** [Go 1.22+](https://go.dev/doc/install) and `make`.

```sh
git clone https://github.com/ssp-data/neomd
cd neomd
make install   # installs to ~/.local/bin/neomd
```

Or just build locally:

```sh
make build
./neomd
```

On first run, neomd:
1. Creates `~/.config/neomd/config.toml` with placeholders — fill in your IMAP/SMTP credentials
2. Creates `~/.config/neomd/lists/` for screener allowlists (or uses your custom paths from config)
3. Creates any missing IMAP folders (ToScreen, Feed, PaperTrail, etc.) automatically

Neomd also runs on Android (more for fun) — see [docs/android.md](docs/android.md).

## Configuration

On first run, neomd creates `~/.config/neomd/config.toml` with placeholders:

```toml
[[accounts]]
name     = "Personal"
imap     = "imap.example.com:993"   # :993 = TLS, :143 = STARTTLS
smtp     = "smtp.example.com:587"
user     = "me@example.com"
password = "app-password"
from     = "Me <me@example.com>"

[screener]
screened_in  = "~/.config/neomd/lists/screened_in.txt"
screened_out = "~/.config/neomd/lists/screened_out.txt"
feed         = "~/.config/neomd/lists/feed.txt"
papertrail   = "~/.config/neomd/lists/papertrail.txt"
spam         = "~/.config/neomd/lists/spam.txt"
```

Use an app-specific password (Gmail, Fastmail, Hostpoint, etc.) rather than your main account password.

For the full configuration reference including multiple accounts, `[[senders]]` aliases, folder customization, signatures, and UI options, see [docs/configuration.md](docs/configuration.md).

## Keybindings

Press `?` inside neomd to open the interactive help overlay. Start typing to filter shortcuts.

See the [full keybindings reference](docs/keybindings.md) (auto-generated from [`internal/ui/keys.go`](internal/ui/keys.go) via `make docs`).

## Screener Workflow

Unknown senders land in `ToScreen`; press `I/O/F/P` to approve, block, or classify them. Auto-screening keeps your inbox clean without manual intervention.

See [docs/screener.md](docs/screener.md) for the full workflow, classification tables, and bulk re-classification instructions.

## How Sending Works

Compose in Markdown, send as `multipart/alternative` (plain text + HTML). Attachments, CC/BCC, multiple From addresses, drafts, and pre-send review are all supported.

See [docs/sending.md](docs/sending.md) for details on MIME structure, attachments, pre-send review, drafts, and how images are handled in the reader.

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
- [go-imap/v2](https://github.com/emersion/go-imap) — IMAP client (RFC 6851 MOVE)
- [go-message](https://github.com/emersion/go-message) — MIME parsing
- [goldmark](https://github.com/yuin/goldmark) — Markdown → HTML for sending
- [BurntSushi/toml](https://github.com/BurntSushi/toml) — config parsing

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for what's new.

## Security

See [SECURITY.md](SECURITY.md) for how credentials, screener lists, temp files, and network connections are handled — with links to the relevant source files.

## Inspirations

- [Neomutt](https://neomutt.org) — the gold standard terminal email client; neomd reuses its screener list format and borrows keybindings (though most are [custom made](https://github.com/sspaeti/dotfiles/blob/master/mutt/.config/mutt/muttrc) and what I use)
- [HEY](https://www.hey.com/features/the-screener/) — the Screener concept: unknown senders wait for a decision before reaching your inbox
- [hey-cli](https://github.com/basecamp/hey-cli) — a Go CLI for HEY; provided the bubbletea patterns used here
- [newsboat](https://newsboat.org) — RSS reader whose `O` open-in-browser binding and vim navigation feel inspired neomd's reader view
- [emailmd.dev](https://www.emailmd.dev) — the idea that email should be written in Markdown when seen on [HN](https://news.ycombinator.com/item?id=47505144)
- [charmbracelet/pop](https://github.com/charmbracelet/pop) — minimal Go email sender from Charm
- [charmbracelet/glamour](https://github.com/charmbracelet/glamour) — Markdown rendering in the terminal
- [kanagawa.nvim](https://github.com/rebelot/kanagawa.nvim) — the color palette used for the inbox
- [msgvault](https://github.com/wesm/msgvault) — Go IMAP archiver; the IMAP client code in neomd is adapted from it

## Disclaimer

This TUI is mostly [vibe-coded](https://www.ssp.sh/brain/vibe-coding) in the sense that all code is written with Claude Code, but guided by very detailed instructions to make the workflow as I use it and like it to be.

I used my experience with Neomutt, TUIs, and the GTD workflow for handling emails with HEY Screener, and added some (hopefully) _taste_ using my favorite tools and aesthetics. Find the full history at [Twitter](https://xcancel.com/sspaeti/status/2036539855182627169#m) - inspired by seeing [Email.md](https://www.emailmd.dev/) on HackerNews.

If you [rather read the prompt](https://www.ssp.sh/brain/id-rather-read-the-prompt), check out my [initial prompt](_prompts/prompt.md) and its generated [plan](_prompts/prompt-plan.md) - which I have iterated and added features by the 100s since then.
## Roadmap

See at my second brain at [Roadmap](https://www.ssp.sh/brain/neomd#roadmap).
