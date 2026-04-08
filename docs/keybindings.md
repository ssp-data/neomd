# Keybindings

Press `?` inside neomd to open the interactive help overlay. Start typing to filter shortcuts.

> The tables below are generated from [`internal/ui/keys.go`](../internal/ui/keys.go).
> To update both the help overlay and this document at once, edit that file and run `make docs`.

<!-- keybindings-start -->

### Navigation

| Key | Action |
|-----|--------|
| `j / k` | move down / up |
| `gg` | jump to top |
| `G` | jump to bottom |
| `enter / l` | open email |
| `h / q / esc` | back to inbox (from reader) |
| `?` | toggle help overlay (type to filter) |


### Folders

| Key | Action |
|-----|--------|
| `L / ] / tab` | next folder tab |
| `H / [ / shift+tab` | previous folder tab |
| `gi` | go to Inbox |
| `ga` | go to Archive |
| `gf` | go to Feed |
| `gp` | go to PaperTrail |
| `gt` | go to Trash |
| `gs` | go to Sent |
| `gk` | go to ToScreen |
| `go` | go to ScreenedOut |
| `gw` | go to Waiting |
| `gm` | go to Someday |
| `gd` | go to Drafts |
| `ge` | go to Everything — latest 50 emails across all folders |
| `gS` | go to Spam (not in tab rotation) |


### Screener  (marked or cursor, any folder)

| Key | Action |
|-----|--------|
| `I` | approve sender → screened_in.txt + move to Inbox (removes from blocked lists) |
| `O` | block sender → screened_out.txt + move to ScreenedOut (removes from screened_in) |
| `$` | mark as Spam → spam.txt + move to Spam (removes from screened_in/out) |
| `F` | mark as Feed → feed.txt + move to Feed |
| `P` | mark as PaperTrail → papertrail.txt + move to PaperTrail |
| `A` | archive (move to Archive, no screener update) |
| `S` | dry-run screen inbox (loaded emails), then y/n |


### Move  (marked or cursor, no screener update)

| Key | Action |
|-----|--------|
| `x` | delete → Trash |
| `Mi` | move to Inbox |
| `Ma` | move to Archive |
| `Mf` | move to Feed |
| `Mp` | move to PaperTrail |
| `Mt` | move to Trash |
| `Mo` | move to ScreenedOut |
| `Mw` | move to Waiting |
| `Mm` | move to Someday |
| `Mk` | move to ToScreen |


### Multi-select & Undo

| Key | Action |
|-----|--------|
| `m` | mark / unmark email + advance cursor |
| `ctrl+u` | clear all marks |
| `U` | undo last move or delete (reverses x, A, M* — not screener actions) |
| `X  (Trash only)` | permanently delete marked or cursor email(s) — no undo |


### Leader Key Mappings (space prefix)

| Key | Action |
|-----|--------|
| `<space>1 … <space>9` | jump to folder tab by number (Inbox=1, ToScreen=2, …) |
| `<space>/` | IMAP search ALL emails on server (From + Subject) |


### Sort  (, prefix)

| Key | Action |
|-----|--------|
| `,m` | date newest first (default) |
| `,M` | date oldest first |
| `,a` | from A→Z |
| `,A` | from Z→A |
| `,s` | size smallest first |
| `,S` | size largest first |
| `,n` | subject A→Z |
| `,N` | subject Z→A |


### Email actions

| Key | Action |
|-----|--------|
| `n` | toggle read/unread  (marked or cursor) |
| `ctrl+n` | mark all in current folder as read |
| `R` | reload / refresh folder |
| `r` | reply  (from inbox or reader) |
| `ctrl+r` | reply-all — reply to sender + all CC recipients  (from inbox or reader) |
| `f` | forward email  (from reader or inbox) |
| `T` | show full conversation thread across folders  (from inbox or reader) |
| `c` | compose new email |
| `ctrl+b  (compose/pre-send)` | toggle Cc+Bcc fields (both hidden by default) |
| `ctrl+f  (compose/pre-send)` | cycle From address through all accounts + [[senders]] aliases |
| `a  (pre-send)` | attach file via yazi file picker (or $NEOMD_FILE_PICKER) |
| `D  (pre-send)` | remove last attachment |
| `d  (pre-send)` | save to Drafts folder (IMAP APPEND with \Draft flag) |
| `s  (pre-send)` | spell check — open in nvim with spell on, jump to first error |
| `p  (pre-send)` | preview email in $BROWSER (images rendered, same as recipient sees) |
| `e  (pre-send)` | re-open editor to edit body |
| `enter  (pre-send)` | confirm and send |
| `1-9  (reader)` | download attachment N to ~/Downloads and open with xdg-open |
| `space+1-9  (reader)` | open link N in $BROWSER (0 = 10th link) |
| `e  (reader)` | open in $EDITOR read-only — search, copy, vim motions |
| `E  (reader)` | continue draft — re-open as editable compose (Drafts folder) |
| `o  (reader)` | open in w3m (terminal browser) |
| `O  (reader)` | open in $BROWSER (GUI browser, images shown) |
| `ctrl+o  (reader)` | open web version / newsletter URL in $BROWSER |
| `ctrl+a  (inbox)` | switch account  (if multiple configured) |


### Command line  (: to open, tab to complete)

| Key | Action |
|-----|--------|
| `:screen  / :s` | dry-run screen loaded inbox emails |
| `:screen-all  / :sa` | dry-run screen ALL inbox emails (no limit) |
| `:reset-toscreen  / :rts` | move all ToScreen emails back to Inbox |
| `:mark-read  / :mr` | mark all emails in current folder as read |
| `:reload  / :r` | reload current folder |
| `:check  / :ch` | show screener classification for selected email |
| `:everything  / :ev` | show latest 50 emails across all folders |
| `:search  / :se` | IMAP search all emails on server (From + Subject + To) |
| `:delete-all  / :da` | permanently delete ALL emails in current folder (y/n) |
| `:empty-trash  / :et` | permanently delete ALL emails in Trash (y/n) |
| `:create-folders  / :cf` | create missing IMAP folders from config (safe, idempotent) |
| `:go-spam  / :spam` | open Spam folder (not in tab rotation) |
| `:debug  / :dbg` | diagnostic report — IMAP ping, config, folders, state (saved to /tmp/neomd/debug.log) |
| `:quit  / :q` | quit neomd |


### Composing

| Key | Action |
|-----|--------|
| `tab  (To/Cc/Bcc)` | accept autocomplete suggestion or next field |
| `ctrl+n / ctrl+p / arrows  (To/Cc/Bcc)` | cycle through address suggestions |
| `enter  (on Subject)` | open $EDITOR with a .md temp file |
| `esc` | cancel |


### General

| Key | Action |
|-----|--------|
| `/` | filter loaded emails (From + Subject, in-memory) |
| `<space>/  or  :search` | IMAP search ALL emails on server (From + Subject) |
| `?` | toggle this help |
| `q` | quit  (from inbox) |

<!-- keybindings-end -->
