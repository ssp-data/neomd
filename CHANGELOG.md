# Changelog

## 2026-03-30

- **Multiple From addresses / SMTP aliases** — add `[[senders]]` blocks to config to define extra From identities (e.g. `s@ssp.sh` as an alias through an existing account's SMTP); cycle through all accounts + senders with `ctrl+f` in both compose and pre-send screens; the `account =` field matches by account `name =` (not email address)
- **Sent folder** — after sending, neomd APPENDs a copy to the configured Sent IMAP folder with `\Seen` flag; the same raw MIME bytes used for SMTP delivery are reused for the APPEND (no double-build)
- **Attachment column in inbox** — `@` appears in a dedicated column next to the date when an email has attachments (detected from IMAP BODYSTRUCTURE including inline images)
- **Attachment downloads in reader** — the email header now lists all attachments as `[1] report.pdf  [2] photo.png`; press `1`–`9` to download attachment N to `~/Downloads/` and open it with `xdg-open`; filenames are deduplicated automatically
- **Inline images as downloads** — images embedded inline in emails (`Content-Disposition: inline`, e.g. PNG screenshots) are now shown alongside regular attachments in the reader header and downloadable with `1`–`9`; previously only `Content-Disposition: attachment` parts were listed
- **Undo move / delete** — `u` reverses the last single or batch move/delete (`x`, `A`, `M*`); uses the UIDPLUS destination UID so undo still works even when the server reassigns UIDs on MOVE; screener actions (`I`, `O`, `F`, `P`, `$`) are intentionally excluded because they also modify `.txt` list files
- **Subject (and headers) re-parsed from editor** — editing `# [neomd: subject: ...]`, `# [neomd: to: ...]`, etc. in neovim now correctly updates those fields; previously the values were captured in a closure before the editor opened and changes were silently discarded; all three editor entry points (new compose, reply, continue draft) now call `editor.ParseHeaders` on the saved file content
- **`ctrl+f` for cycling From** — changed from `f` (which conflicts with typing in text fields) to `ctrl+f`; works in both the compose form and the pre-send review screen

## 2026-03-29

- **CC field** — compose and reply forms now include an optional Cc field (Tab/Enter to skip); CC recipients receive the email and appear in the `Cc:` header
- **BCC field** — hidden by default; toggle with `ctrl+b` in compose; BCC recipients receive the email but are not visible in the message headers (standard BCC privacy)
- **Reply-all** — `R` in the reader replies to the original sender + all CC recipients; your own address is excluded automatically; uses `Reply-To` header when present
- **Pre-send review screen** — after closing the editor, neomd shows a summary (To, Subject, body preview) before sending; press `enter` to send, `a` to attach files via yazi (auto-detected, no config needed; override with `$NEOMD_FILE_PICKER`), `D` to remove last attachment, `d` to save to Drafts, `e` to re-open the editor, `esc` to cancel; avoids tmux/terminal key-capture issues since `a` needs no modifier
- **Save to Drafts** — `d` in the pre-send screen APPENDs the composed message to the configured Drafts IMAP folder with `\Draft` + `\Seen` flags; navigate to it with `gd`
- **Attachments from neovim** — `<leader>a` in a `neomd-*.md` buffer opens yazi in a floating terminal; selected files are inserted as `[attach] /path/to/file` lines (visible in markdown, not hidden HTML comments); neomd strips them before sending and adds them as MIME attachments
- **Inline code and code blocks** — `` `inline code` `` and fenced ` ``` ` blocks are rendered in HTML emails (goldmark CommonMark + GFM; styled with monospace font and light grey background)

## 2026-03-27

- **`gd` Drafts navigation** — jump to Drafts folder with `gd` even when it's not in the tab rotation
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
