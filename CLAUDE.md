# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Dev Commands

- `make build` — compile to `./neomd` (also regenerates `docs/keybindings.md` from `internal/ui/keys.go`)
- `make run ARGS="..."` — build and run
- `make install` — install to `~/.local/bin/neomd` (default target)
- `make test` — `go test ./...` (unit tests, no network)
- `make test-integration` — integration tests against real IMAP/SMTP (requires demo account env vars)
- `make vet` / `make fmt` / `make tidy`
- `make docs` — regenerate keybindings doc from `internal/ui/keys.go` (runs as part of `build`) and sync README to docs site
- `make docs-serve` — serve Hugo docs locally at http://localhost:1313
- `make docs-build` — build Hugo docs site to `docs/public/`
- `make send-test TO=addr` — run `./cmd/sendtest` to send a test email
- `make demo` / `make demo-hp` — run with demo configs at `~/.config/neomd-demo/` and `~/.config/neomd-demo-hostpoint/`
- `make benchmark` — IMAP latency benchmark (requires `IMAP_PASS_SIMU`, `IMAP_APPPASS_GMAIL_NEOMD` env vars)
- `make android` — cross-compile ARM64 for Termux
- `make release VERSION=v0.1.0` — tag and push a new release (runs docs build, GitHub Actions handles publishing)
- Single test: `go test ./internal/smtp -run TestBuildMessage`

Requires Go 1.22+. Binary version is injected via `-ldflags -X main.version=$(git describe)`.

## Architecture

**Entry point:** `cmd/neomd/main.go` wires config → IMAP client → bubbletea program. Two side CLIs: `cmd/docs` (regenerates keybindings doc) and `cmd/sendtest` (sender smoke test).

**TUI is a single bubbletea Model** in `internal/ui/model.go` with a `viewState` enum state machine: `stateInbox`, `stateReading`, `stateCompose`, `statePresend`, `stateHelp`. All state transitions flow through `Update()`. Sibling files specialize views but share the one model:
- `inbox.go` — folder tabs, list, threading connector rendering
- `reader.go` — glamour-rendered email, attachments, numbered links
- `compose.go` — multi-step compose form (To/CC/BCC/Subject)
- `search.go`, `cmdline.go`, `thread.go`, `keys.go`, `styles.go`

**Keybindings are declared once** in `internal/ui/keys.go` and drive both the in-app help overlay and the generated `docs/keybindings.md`. When adding a binding, edit that table — do not hand-edit the markdown docs.

**Compose flow:** user's `$EDITOR` (nvim) opens a temp `neomd-*.md` file with a prelude of `# [neomd: to: ...]` / `# [neomd: subject: ...]` headers built by `internal/editor/editor.go`. On editor exit, parsing extracts headers and `[attach] /path` inline lines (plain-text marker, NOT HTML comments — treesitter hides those). Then `statePresend` shows a review screen before sending.

**MIME structure** (`internal/smtp/sender.go`, `BuildMessage` is the exported entry point, reused by draft-save):
- no attachments → `multipart/alternative` (text/plain + goldmark HTML)
- file attachments only → `multipart/mixed > multipart/alternative`
- inline images only → `multipart/related > (alternative + image parts with Content-ID)`
- both → `multipart/mixed > (multipart/related > alt+images) + file parts`
- Image extensions: `.png .jpg .jpeg .gif .webp .svg`. `imgSrcRe` rewrites local `<img src="/abs/path">` to `cid:` refs.

**IMAP client** (`internal/imap/client.go`) uses `go-imap/v2` (beta). Known API quirks to preserve:
- `imapclient.FetchMessageBuffer` (not `FetchedMessage`)
- `conn.Copy()` / `conn.Store()` (not UID-prefixed variants)
- `BodySection[0].Bytes` for raw MIME
- APPEND pattern: `conn.Append(folder, size, opts)` → `.Write(raw)` → `.Close()` → `.Wait()`
- `go-message` v0.18.2: `mail.PartHeader` lacks `ContentType()` — type-assert to `*mail.InlineHeader` / `*mail.AttachmentHeader`
- `bubbletea` v1.3.10: key type is `tea.KeyMsg` (not `KeyPressMsg`)

Folder operations prefer RFC 6851 MOVE; `u` undo uses UIDPLUS destination UIDs captured on move/delete.

**Screener** (`internal/screener/`) reads line-based lists of email addresses from paths defined in config. Default paths are under `~/.config/neomd/lists/`. Classification (`I`/`O`/`F`/`P`) appends to the corresponding list file and moves the message to the matching folder. Auto-screening runs on Inbox load and on a 5-minute background timer (`ui.background_sync_interval`).

**Config** (`internal/config/`) — TOML at `~/.config/neomd/config.toml`, auto-created with placeholders. Supports multiple `[[accounts]]` and SMTP-only `[[senders]]` aliases (cycled with `ctrl+f` in compose/pre-send). OAuth2 authentication supported via `oauth2_client_id`, `oauth2_client_secret`, `oauth2_issuer_url`, `oauth2_scopes` fields. `-config PATH` flag overrides location.

**Documentation** — Hugo site in `docs/` served at https://ssp-data.github.io/neomd/. README.md is synced to `docs/content/overview.md` via `scripts/sync-readme-to-docs.sh`. Keybindings are auto-generated from `internal/ui/keys.go` via `cmd/docs/main.go` — never hand-edit the markdown tables.

**Package structure:**
- `internal/ui/` — bubbletea TUI: model.go (state machine), inbox.go, reader.go, compose.go, keys.go (single source of truth for keybindings)
- `internal/imap/` — IMAP client wrapper using go-imap/v2
- `internal/smtp/` — email sender, MIME builder (`BuildMessage` is the main entry point)
- `internal/screener/` — HEY-style sender classification
- `internal/config/` — TOML config parsing
- `internal/editor/` — spawns $EDITOR with neomd-*.md temp files
- `internal/render/` — glamour-based Markdown rendering for terminal
- `internal/mailtls/` — TLS/STARTTLS connection helpers
- `internal/oauth2/` — OAuth2 flow for Gmail/Office365

## Project-Specific Conventions

- **Keep diffs minimal** — fix the specific thing asked; do not refactor adjacent code.
- **Avoid modifier keys for new bindings** — user's tmux prefix is `C-t`, and `ctrl+a`/`ctrl+e` collide with bubbles textinput line-start/end. Prefer plain letters, especially on the pre-send screen.
- **Inline markers must be visible plain text** — use `[attach] /path`, never HTML comments (hidden by treesitter in the neovim compose buffer).
- **Neovim integration lives in dotfiles**, not in this repo: `/home/sspaeti/git/general/dotfiles/nvim/.config/nvim/lua/sspaeti/custom.lua` defines the `<leader>a` yazi picker scoped to `BufEnter neomd-*.md`.
- Screener list files historically live at `~/.dotfiles/neomd/.lists/` for this user (overridden in their config), though the default for new installs is `~/.config/neomd/lists/`.
