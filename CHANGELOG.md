# Changelog

# 2026-04-16
- **Redesigned welcome screen** — new two-column layout with ASCII art logo, philosophy/getting started guide on the left, and essential shortcuts organized by category on the right; wider box (100 chars) with cleaner spacing; maintains kanagawa color scheme; more scannable and visually appealing for new users
- **ASCII logo in help overlay** — pressing `?` now shows the neomd ASCII art logo overlaid on the top-right corner of the help screen; shortcuts start immediately at the top without vertical space taken by the logo; logo only appears when scrolled to the top
- **`space+w` welcome shortcut** — press `space` then `w` to reopen the welcome screen anytime; useful for reviewing keybindings and getting started guide; documented in help overlay and keybindings reference
- **`N` jump to next unread** — press `N` to jump to the next unread email in the current folder; wraps around to the beginning if no unread found after cursor; displays status message if no unread emails exist
- **`z` toggle unread-only view** — press `z` to filter the inbox to show only unread emails (mnemonic: "zero in on unread"); press `z` again to show all emails; works alongside text filter (`/`) and can be cleared with `esc`; status bar indicates current view mode
- **Fix: help menu search** — all keys (including `j`, `k`, `d`, `u`) are now available for typing when searching in help overlay (`?` then `/`); scroll keys only work when not in search mode

# 2026-04-15
- **Scheduled folder keybindings** — added `gc` (go to Scheduled, mnemonic: "calendar") and `Mc` (move to Scheduled) shortcuts; Scheduled folder now accessible via dedicated keybindings alongside existing tab navigation (`[]HL`, `space+1-9`); help overlay and generated keybindings documentation updated

# 2026-04-14
- **Extended link support (99 links)** — link opener now supports up to 99 links per email (previously limited to 10); `space+1-0` opens links 1-10, `space+l11-99` opens links 11-99 using intuitive numeric shortcuts (e.g. `space+l26` for link [26]); status line provides progressive feedback during multi-key input; footer help and `?` overlay updated
- **Fix: link extraction with brackets in text** — markdown link regex now correctly matches links with brackets inside the link text (e.g. `[[Watch the studio tour here]](url)`); changed from `[^\]]+` (anything except `]`) to non-greedy `.+?` to handle nested brackets; fixes newsletter links from Beehiiv and similar services

# 2026-04-13
- **Emoji reactions (`ctrl+e`)** — fast, keyboard-driven emoji reactions from inbox or reader; press `ctrl+e` to open emoji picker overlay, select with `1`-`8` for instant send or navigate with `j`/`k` and press `enter`; sends minimal reaction email (emoji + italic footer + quoted original message) with proper threading headers; available reactions: 👍 ❤️ 😂 🎉 🙏 💯 👀 ✅; original email marked with `\Answered` flag; reaction saved to Sent folder; auto-selects From address matching recipient (same logic as regular replies)
- **Email threading headers** — all replies (regular `r`/`R` and emoji reactions `ctrl+e`) now include proper `In-Reply-To` and `References` headers for conversation threading; ensures replies appear correctly grouped in Gmail, Outlook, and Apple Mail conversation views; `References` header extracted from IMAP message body and preserved in reply chain
- **Fix: refresh not showing new emails immediately** — pressing `R` now correctly displays new emails on first refresh; previously the IMAP client cached the selected mailbox state, so the first `R` would skip re-SELECT and use stale UID SEARCH results (showing the old unread count but no new messages in the list); required a second `R` or tab switch to see new emails; now forces a fresh SELECT to ensure mailbox state is current; also fixed background sync path to prevent stale cache; added regression test (internal/imap/client_test.go:410)


# 2026-04-10
- **HTML signature support** — new `[ui.signature_block]` config with separate `text` and `html` fields for dual-format signatures; text signature appears in the editor and text/plain MIME part, HTML signature appends to the text/html part only; use `[html-signature]` placeholder in text signature to control HTML signature inclusion per-email (visible in preview, deletable before sending); backward compatible with legacy `signature` field
- **Fix: draft formatting corruption** — drafts are now stored as plain text only instead of multipart/alternative to prevent HTML→markdown conversion artifacts; fixes line break addition, pipe escaping (`|` → `\|`), and italic style changes (`*` → `_`) when reopening saved drafts
- **Sent/Drafts primary-account default restored** — in multi-account setups, Sent and Drafts now default back to the first configured IMAP account while SMTP still uses the selected sending identity; added `store_sent_drafts_in_sending_account = true` for users who want Sent/Drafts to follow the sending account instead
- **Proton Mail Bridge compatibility** — documented that Proton Mail works with neomd only via Proton Mail Bridge (paid Proton feature), added optional `tls_cert_file` support for trusting Bridge’s exported self-signed certificate, and added a narrow localhost-only TLS retry fallback for Bridge connections on `127.0.0.1`/`localhost`; normal remote IMAP/SMTP providers keep their existing strict certificate verification behavior
- **Issue #6 verification pass** — reviewed the user report against the current code and  specifically verified that startup auto-screening does not route Inbox mail to Trash in the current implementation, while manual `ToScreen` screening remains message-by-message by design
- **Fix: Drafts/Spam reload off-tab folder mismatch** — reloading while viewing an off-tab folder now reloads that actual mailbox instead of the currently selected tab's folder; fixes the confusing case where Drafts could show Inbox content after pressing `R`
- **Fix: committed `/` filter now clears with `esc`** — pressing `esc` now reliably clears the in-memory inbox filter even after the filter was already applied
- **Help overlay improvements** — `?` help is now scrollable with `j/k`, arrow keys, and `d/u`; search begins only after pressing `/`, so opening help no longer immediately behaves like a search prompt
- **Attachment workflow guidance** — startup/welcome messaging now warns when the optional inline Neovim attachment integration is unavailable; README install docs now list `yazi` and the external `custom.lua` integration as optional requirements for `<leader>a`, while clarifying that pre-send `a` still works independently
- **UX hints** — inbox footer now exposes `, sort`; pre-send footer clarifies `s` as spell-check-and-edit versus plain `e` edit; compose/pre-send `ctrl+f` now shows a message when only one From identity is configured
- **Fix: sender-level screening from `ToScreen`** — approving/blocking/feed/papertrail/spam on a single unmarked message in `ToScreen` now expands to all currently queued mail from that sender, matching the intended HEY-style workflow
- **Safety guard: screener destinations may not point to Trash** — screening now refuses to run if `ToScreen`, `ScreenedOut`, `Feed`, `PaperTrail`, or `Spam` are configured to the same IMAP folder as Trash
- **Inbox paging clarity** — the inbox header now shows the current fetch limit (`loaded/limit`) and `d/u` page movement directly, so the “only 50 emails” behavior is visible without guessing
- **Discard confirmation for unsent mail** — `esc` in compose and `esc`/`x` in pre-send now ask for confirmation before dropping the message; recovery hints still point to `:recover`
- **Default Inbox load raised to 200** — new configs now use `inbox_count = 200`; README, config docs, and welcome text now clarify that normal loads/auto-screening only process that loaded Inbox slice, while `:screen-all` scans the full Inbox on the IMAP server
- **Compose/draft round-trip preservation** — editor/pre-send/draft/recover flows now preserve `Bcc` and selected `From`; continuing a draft also restores its attachments back into the compose session
- **Correct IMAP account for Sent/Drafts** — sent copies and saved drafts now use the IMAP account that matches the selected sending identity / `[[senders]]` alias instead of always using the currently active inbox account
- **Draft MIME keeps `Bcc`** — Drafts saved via IMAP now retain the `Bcc` header so reopening a draft does not silently lose hidden recipients
- **Search/Everything/Thread subjects no longer mutate** — folder prefixes are now display-only in list rendering, so reply/forward/thread logic keeps using the real RFC subject
- **Screener rollback safety** — screener actions now snapshot list state and roll back both list files and already-moved emails if a later move fails, keeping mailbox state and screener files consistent
- **`:search` help text fixed** — the command description now correctly says it searches across configured folders, not just the current folder

## Roborev 
- **Security: path traversal vulnerability fixed** — inline image handling (`O` browser preview) now sanitizes `ContentID` and `Filename` from email MIME headers to prevent attackers from writing files outside `/tmp/neomd/` via malicious `cid:` references (e.g. `../../etc/cron.d/evil`); all attachment paths now use `filepath.Base()` and verify the result stays under temp directory before writing
- **Fix: conversation view navigation** — pressing `T` (thread view) now correctly shows error messages and empty-result warnings; `imapSearchResults` flag is cleared immediately so the status bar appears instead of the search bar; added general Esc handler for `offTabFolder` views that preserves search context: pressing Esc from thread view returns to IMAP search results if that's where you came from (checked via `imapSearchText`), otherwise returns to active folder; `imapSearchText` is cleared when navigating away from search via tab, clicks, or go-to commands (gi/ga/etc) to prevent stale search context from affecting unrelated views; search retry errors are now visible because `imapSearchResults` is only set by the handler on success
- **Fix: Work folder move guard** — `Mb` (move to Work) is now disabled when the Work folder is not configured, preventing moves to an empty folder name; previously caused silent failures
- **Fix: Work folder keybindings in help** — `gb` (go to Work) and `Mb` (move to Work) now appear in the `?` help overlay and generated keybindings documentation, marked as "(if configured)" to indicate they're optional
- **Test coverage: expandEnv edge cases** — added unit tests for environment variable expansion covering unset variables (silently return empty), bare `$` alone, empty `${}`, whitespace trimming, and variables with text suffixes/prefixes; documents current behavior for config password/user fields
- **Fix: welcome message formatting** — onboarding screen instruction now reads clearly ("Go to Inbox tab; once screener is active, use ToScreen") instead of the previous formatting regression ("ToScreentab")


# 2026-04-09
- **Fix: non-standard IMAP/SMTP ports** — neomd now correctly handles non-standard ports (e.g., Proton Mail Bridge on `127.0.0.1:1143` and `127.0.0.1:1025`); previously hardcoded port-based logic ignored the user's `starttls` config and refused unencrypted connections to any port other than 993/143 (IMAP) or 465/587 (SMTP); new behavior: user's explicit `starttls = true` always forces STARTTLS, standard ports use their defaults (993→TLS, 143→STARTTLS, 465→TLS, 587→STARTTLS), non-standard ports default to TLS for security (user must set `starttls = true` if their provider uses STARTTLS on a custom port); fixes "refusing unencrypted connection to 127.0.0.1:1143" error reported by Proton Bridge users; comprehensive test coverage added for all port/config combinations

# 2026-04-08
- **Fix: pre-send `e` losing email body** — pressing `e` in the pre-send review to re-edit now correctly reopens the editor with the existing body; previously it opened a blank compose with only the signature, silently discarding the email content (including reply history)
- **Draft backups** — every compose session is automatically backed up to `~/.cache/neomd/drafts/` before the temp file is deleted; keeps a rolling 20 backups (configurable via `draft_backup_count` in `[ui]`, set to `-1` to disable); no more lost emails after crashes or accidental closes
- **`:recover` / `:rec` command** — reopens the most recent draft backup as a compose session; To/Cc/Bcc/Subject are parsed from the backup and pre-filled automatically
- **Screener docs: "screening happens once"** — documented that auto-screening only runs on the Inbox folder; emails moved to ToScreen by another device are not re-classified; use `:reset-toscreen` to move them back for re-screening
- **Test suite** — 147 unit tests across 8 packages covering screener classification, MIME message building, editor parsing, config loading, IMAP search, OAuth2 token handling, rendering, and security invariants (file permissions, BCC privacy, credential leak prevention); CI workflow runs `go test` + `go vet` on every PR
- **Integration tests** (`make test-integration`) — end-to-end tests against a real IMAP/SMTP server: send plain email and verify From/To/Subject/HTML body round-trip, CC header, file attachment content, non-ASCII subject encoding (umlauts + emoji), IMAP search with `from:`/`subject:` prefixes, move + undo, inline images, signature HTML rendering, SaveSent IMAP APPEND, comma-separated multiple recipients, and reply-all with 3 distinct addresses; all test emails cleaned up automatically; skipped without credentials so `make test` stays fast and offline
- **Fix: multiple To recipients** — `Send()` now correctly splits comma-separated To addresses into individual SMTP RCPT TO commands; previously the entire `"a@x.com, b@x.com"` string was passed as a single address, causing delivery failures
- **Fix: To/CC display** — reader and inbox now show all To and CC addresses, not just the first; `FetchHeadersByUID` (used by search/everything) now also populates To and CC fields
- **Reply-all rebind to `ctrl+r`** — `R` (Shift+R) is now consistently reload/refresh in all views; reply-all moved to `ctrl+r` which works from both inbox list and reader (previously `R` conflicted between reload in inbox and reply-all in reader)
- **Default signature for new users** — new installs get `*sent from [neomd](https://neomd.ssp.sh)*` as the default signature
- **Reply indicator (`·`)** — emails you've replied to show a `·` dot in the inbox list between the flag and thread columns; uses the standard IMAP `\Answered` flag so it works across clients (reply from webmail → neomd shows it)
- **`\Answered` flag on reply** — after sending a reply, the original email is automatically marked as `\Answered` on the IMAP server
- **Conversation thread view (`T` / `:thread`)** — press `T` from inbox list or reader to see the full conversation across folders (Inbox, Sent, Archive, Waiting, Work, etc.); searches by normalized subject + participant overlap; displays in a temporary "Thread" tab with `[Folder]` prefix and `│`/`╰` threading connectors; esc returns to previous view
- **Custom folder support (`work`)** — optional `work = "Work"` in `[folders]` config; add `"work"` to `tab_order` to show as a tab; `gb` to go, `Mb` to move; auto-created on first run if configured; included in Everything, Search, and conversation views
- **Inline images in browser preview** — pressing `O` to open an email in the browser now shows inline images from other senders; `cid:` references are rewritten to temp files so the browser can display them; previously only your own sent emails rendered images correctly
- **`compose_editor` config option** — optional `compose_editor` in `[ui]` to use a different editor for compose/reply/forward (e.g. `"nvim --appname nvim-wp"`); defaults to `$EDITOR` / `nvim`

# 2026-04-05
- **OAuth2 authentication** ([#3](https://github.com/ssp-data/neomd/pull/3), thanks [@notthatjesus](https://github.com/notthatjesus)) — accounts can set `auth_type = "oauth2"` with `oauth2_client_id`, `oauth2_client_secret`, `oauth2_issuer_url`, and `oauth2_scopes` instead of a password; on first launch neomd opens the browser for the authorization code flow, persists the token to `~/.config/neomd/tokens/<account>.json`, and refreshes it automatically; works with Gmail, Office365, and any OIDC-discoverable provider via XOAUTH2 over IMAP and SMTP; password auth paths unchanged for existing accounts
- **`auto_bcc` config** — root-level `auto_bcc = "addr@example.com"` appends an address to every outgoing email's Bcc so you keep a copy in an external mailbox (e.g. a hey.com archive); visible in the composer and pre-send review (no silent BCC), deduped against any manual Bcc entry
- **`shift+tab` in compose** — navigate back through To/Cc/Bcc/Subject fields (previously could only move forward with tab/enter)
- **Reader shows local time** — email dates in the reader header now convert to your system's local timezone and include the clock time (e.g. `Apr 05, 00:51`); previously showed the sender's timezone date without time

# 2026-04-02
- **Auto From on reply** — replying auto-selects the From address that matches the email's To/CC field (e.g. email sent to `simon@domain.com` replies from `simon@domain.com`); `r` now works from inbox list view; `# [neomd: from: ...]` shown in editor; `x` in pre-send discards the email
- **Email safety hardening** — bulk operations show live progress counter ("Screening: 42/1000…") for batches >10; screener now moves emails before updating list files (no inconsistent state on failure); SaveSent failure shown as warning instead of silently swallowed; batch failures report exact moved/total counts; partial batch undo info preserved on error; undo stack capped at 20
- **Screener lists created on startup** — all 5 screener `.txt` files are created as empty files on first run (alongside directories), consistent with IMAP folder creation
- **Config-isolated cache** — demo and production configs use separate cache directories (derived from config dir name), so `make demo-reset` never touches production data
- Added benchmark to readme as Gmail was considerly slower than my IMAP provider here from Switzerland.

# 2026-04-01
- **Threaded inbox** — related emails are automatically grouped in the inbox list with a Twitter-style vertical connector line (`│`/`╰`); threads detected via `In-Reply-To`/`Message-ID` IMAP envelope headers with a reply-prefix subject fallback (only emails with `Re:`, `AW:`, `Fwd:` etc. are grouped by subject — recurring notifications/invoices stay separate); newest reply on top, root at bottom; threads sorted by most recent email so active conversations float to the top
- **Clickable tabs** — folder tabs in the top bar are clickable with the mouse; click any tab to switch folders
- **Spell check in pre-send (`s`)** — opens nvim with spell checking enabled (`en_us` + `de`), cursor jumps to the first misspelled word; use `]s`/`[s` to navigate errors, `z=` for suggestions, `zg` to add to dictionary; corrected body flows back to pre-send
- **`:debug` / `:dbg` command** — writes a diagnostic report covering IMAP connectivity (ping test), account config (emails masked), folder mapping, screener list status, UI config, and current state; opens in the reader and saves to `/tmp/neomd/debug.log` for sharing; no sensitive data (passwords, full emails) included
- **Drafts show recipient** — Drafts folder now shows `→ recipient` instead of From (same as Sent tab), since all drafts are from you
- **`ctrl+b` in pre-send** — toggle CC/BCC fields from the pre-send review screen (previously only available during compose)
- **`u` / `U` rebind** — `u` is now free for page-up (vim-style half-page scroll); `U` is undo last move/delete; `ctrl+u` clears all marks
- **Temp files in `/tmp/neomd/`** — all temp files (compose, preview, spell check) now live in `/tmp/neomd/` subdirectory for easy recovery after crashes and less clutter
- **Improved onboarding** — auto-screening is now paused when screener lists are empty (first run), preventing all emails from being moved to ToScreen; activates automatically once the user classifies their first sender; welcome screen rewritten with step-by-step getting-started guide explaining the screener workflow, batch operations (`m` + `I`), and config hints (`:debug`, `auto_screen_on_load`)
- **`]` / `[` folder navigation** — bracket keys now switch to next/previous folder tab (alongside `L`/`H` and `tab`/`shift+tab`)

## 2026-03-31
- fix showing recipient in SENT tab (instead of from)
- **IMAP search across all folders (`space /` or `:search`)** — server-side IMAP SEARCH across all configured folders (Inbox, Sent, Archive, Feed, etc.); results displayed in a temporary "Search" tab with `[Folder]` prefix on each subject; supports query prefixes: `from:simon`, `subject:invoice`, `to:team@`, or plain text to search all three fields; press `esc` to close results
- **Filter preserves across actions** — the local `/` filter no longer clears when pressing `n` (toggle read), `m` (mark), `U` (clear marks), or sorting; filter stays active until `esc`
- **Address autocomplete in compose** — To, Cc, and Bcc fields show autocomplete suggestions from screener lists (`screened_in.txt`, `feed.txt`, `papertrail.txt`); navigate with `ctrl+n`/`ctrl+p`/arrows, accept with `tab`; supports multi-address fields (autocomplete applies after the last comma)
- **Everything view (`ge` or `:everything`)** — shows the 50 most recent emails across all folders in a temporary "Everything" tab, sorted by date descending; each subject prefixed with `[Folder]`; useful for finding emails that were screened out or moved to spam
- **Link opener (`space+1-9` in reader)** — links are extracted from the email body, numbered `[1]`-`[0]` in the header; press `space` then a digit to open in `$BROWSER`; up to 10 links per email, deduplicated by URL
- **Draft signature fix** — re-opening a draft (`E`) no longer appends a duplicate signature; the draft body already contains it from the first compose
- **Draft reader footer** — `E draft` now appears in the reader footer when viewing an email from the Drafts folder
- **Android support (`make android`)** — cross-compile for Android ARM64; runs in Termux; documented in `docs/android.md` with install instructions and useful shortcuts
- **Docs restructure** — detailed documentation moved from README to `docs/` folder: `docs/keybindings.md` (auto-generated), `docs/screener.md`, `docs/sending.md`, `docs/configuration.md`, `docs/android.md`; README kept concise with links

## 2026-03-30

- added  preview email in $BROWSER (images rendered, same as recipient sees)  with `p`
- **Multiple From addresses / SMTP aliases** — add `[[senders]]` blocks to config to define extra From identities (e.g. `s@ssp.sh` as an alias through an existing account's SMTP); cycle through all accounts + senders with `ctrl+f` in both compose and pre-send screens; the `account =` field matches by account `name =` (not email address)
- **Sent folder** — after sending, neomd APPENDs a copy to the configured Sent IMAP folder with `\Seen` flag; the same raw MIME bytes used for SMTP delivery are reused for the APPEND (no double-build)
- **Attachment column in inbox** — `@` appears in a dedicated column next to the date when an email has attachments (detected from IMAP BODYSTRUCTURE including inline images)
- **Attachment downloads in reader** — the email header now lists all attachments as `[1] report.pdf  [2] photo.png`; press `1`–`9` to download attachment N to `~/Downloads/` and open it with `xdg-open`; filenames are deduplicated automatically
- **Inline images as downloads** — images embedded inline in emails (`Content-Disposition: inline`, e.g. PNG screenshots) are now shown alongside regular attachments in the reader header and downloadable with `1`–`9`; previously only `Content-Disposition: attachment` parts were listed
- **Inline image placeholders in reader body** — `<img src="cid:...">` tags now show `[Image: filename.png]` at their position in the body text instead of being silently stripped; uses Content-ID → filename mapping from MIME parts
- **Undo move / delete** — `u` reverses the last single or batch move/delete (`x`, `A`, `M*`); uses the UIDPLUS destination UID so undo still works even when the server reassigns UIDs on MOVE; screener actions (`I`, `O`, `F`, `P`, `$`) are intentionally excluded because they also modify `.txt` list files
- **Subject (and headers) re-parsed from editor** — editing `# [neomd: subject: ...]`, `# [neomd: to: ...]`, etc. in neovim now correctly updates those fields; previously the values were captured in a closure before the editor opened and changes were silently discarded; all three editor entry points (new compose, reply, continue draft) now call `editor.ParseHeaders` on the saved file content
- **`ctrl+f` for cycling From** — changed from `f` (which conflicts with typing in text fields) to `ctrl+f`; works in both the compose form and the pre-send review screen
- **Forward (`f`)** — forward an email from the reader or inbox; opens the editor with the original message quoted, `Fwd:` subject prefix, and empty `To:` field; from inbox the body is fetched automatically before opening the editor
- **Permanent delete (`X`, Trash only)** — permanently deletes marked or cursor email(s) from the Trash folder via IMAP STORE `\Deleted` + UID EXPUNGE; blocked in other folders with a warning message
- **`:empty-trash` / `:et`** — permanently delete all emails in Trash with y/n confirmation; works from any folder without navigating to Trash first
- **First-run welcome popup** — on the very first launch, a centered popup shows quick-start keybindings and screener basics; any key dismisses it; marker at `~/.cache/neomd/welcome-shown` ensures it only appears once
- **Auto-create IMAP folders on startup** — `ensureFoldersCmd` runs during `Init()` so new users don't need to manually run `:create-folders`; idempotent for existing users
- **Auto-create screener list directories** — parent directories for screener list paths are created automatically during config load; prevents errors when pressing `I`/`O`/`F`/`P` on a fresh install
- **Default screener paths** — changed from `~/.config/mutt/` to `~/.config/neomd/lists/` for new installs; existing configs with custom paths are unaffected
- **Go prerequisite check in Makefile** — `make build`/`make install` now prints clear Go installation instructions instead of a cryptic error when `go` is not found
- **Pre-send preview (`p`)** — press `p` in the pre-send screen to open a browser preview of the composed email; renders through the same goldmark pipeline as sending, with local image paths converted to `file://` URLs so inline images from `[attach]` lines display correctly

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
