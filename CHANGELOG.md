# Changelog

## 2026-03-27

- **`gd` Drafts navigation** — jump to Drafts folder with `gd` even when it's not in the tab rotation (save-to-draft not yet implemented, but you can read existing drafts)
- **Off-tab folder indicator** — when viewing Spam (`gS`) or Drafts (`gd`), the folder name appears highlighted in the tab bar with a `│` separator; no regular tab stays falsely active
- **Security hardening** — IMAP refuses unencrypted connections (non-993/143 ports error out instead of `DialInsecure`); email-extracted URLs validated to `http/https` only before opening in browser (case-insensitive, RFC 3986); `SECURITY.md` added documenting credential storage, TLS guarantees, screener list handling, and temp file lifecycle with links to source
- **Spam folder** — `$` marks a sender as spam (writes to `spam.txt`, moves to Spam IMAP folder). Separate from ScreenedOut so you never have to look at it again. Navigate with `gS` or `:go-spam` — kept out of the tab rotation intentionally
- **Cross-list cleanup** — reclassifying a sender removes them from conflicting lists automatically: `I` (approve) removes from screened_out + spam; `O` (block) removes from screened_in; `$` (spam) removes from screened_in + screened_out. No manual `.txt` editing needed
- **`:` command history** — `↑`/`↓` cycles through the last 5 distinct commands; `→` accepts the ghost completion; `ctrl+n`/`ctrl+p` cycle forward/backward through completions. Persists across restarts in `~/.cache/neomd/cmd_history` (outside dotfiles version control)
- **Leader key** — `space` is the leader; `<space>1`–`<space>9` jumps to a folder tab by number
- **Auto-screen on inbox load** — screener applies automatically on every Inbox load (startup, `R`). Disable with `auto_screen_on_load = false` in `[ui]`
- **Background sync** — inbox re-fetched and screened every 5 minutes while neomd is open. Configure with `bg_sync_interval` in `[ui]`; `0` disables it
- **`n` / `m` rebind** — `n` toggles read/unread (was `N`); `m` marks for batch ops (was `space`)

## 2026-03-25

- **Signature** — auto-appended to new compose buffers; configure in `[ui]` with `signature`
- **Compose abort** — closing the editor with `ZQ` / `:q!` cancels the email; only `ZZ` / `:wq` sends
- **Browser image workflow** — `O` opens email as HTML in `$BROWSER`; `ctrl+o` opens the canonical web/newsletter URL (extracted from `List-Post` header); `o` opens in w3m
- **`:create-folders` / `:cf`** — creates any missing IMAP folders defined in config (idempotent)
