# Merge threads (HEY-style) — design

Date: 2026-09-18

## Problem

Recurring unrelated emails (typically mailer-daemon bounces) share no
In-Reply-To or `Re:` subject, so the automatic threading in
`internal/ui/thread.go` never groups them. They fill the Inbox; archiving
them buries the important ones in Archive. HEY solves this with
"merge threads": the user picks emails, gives the merge a title, and they
show as one conversation from then on.

## Goals

- User picks emails, names the merge, and they collapse into one row.
- Opening the row shows every member, across folders, as a thread list.
- A merge may carry a sender rule so future emails from that sender join
  automatically.
- Nothing else changes: automatic threading, reply indicators, reader,
  compose, screener, headless daemon, existing keys.

## Non-goals

- Subject rules, regex rules, or rules on anything but the From address.
- Syncing merges through IMAP keywords. The store is a local file.
- Renaming a merge from the TUI (edit the file).

## Store — `internal/merge/`

File: `~/.config/neomd/merges.toml`, resolved with the same
config-name-aware helper pattern as `SpyPixelCachePath()` so demo
configs get their own file. Human-editable, so it can live in dotfiles.

```toml
[[merges]]
title = "Bounces"
sender = "mailer-daemon@"         # optional; case-insensitive substring of the From address
message_ids = ["<a@x>", "<b@y>"]
```

API (pure, no IMAP):

- `Load(path) (*Store, error)` — missing file is an empty store.
- `Save() error` — atomic write (temp file + rename).
- `Add(title string, ids ...string)` — creates the merge if missing,
  de-duplicates ids.
- `SetSender(title, addr string)` — creates if missing.
- `Remove(id string)` — drops one id from whichever merge holds it.
- `Dissolve(title string)`.
- `TitleOf(id string) (string, bool)`.
- `MatchSender(from string) (title string, ok bool)` — first rule whose
  substring matches the normalized From address.
- `Titles() []string` — for tab completion.

Message-ID is the key because it survives folder moves. UIDs are not
stored.

## Commands (`internal/ui/cmdline.go`)

| Command | Effect |
|---|---|
| `:merge <title>` | Add marked-or-cursor emails (`targetEmails()`) to the merge, creating it. Cursor on a collapsed row adds all its members, which persists absorbed replies as explicit members. |
| `:merge-sender <title>` | Same as `:merge`, plus stores the cursor email's From address (via `normalizedSender`) as the sender rule. |
| `:unmerge` | On a collapsed row: y/n, then dissolve the merge. Inside an opened merge view: remove the cursor email's Message-ID only. Elsewhere: status error. |

Tab completion offers existing titles after `:merge ` and
`:merge-sender `. Titles may contain spaces; everything after the command
word is the title. Emails without a Message-ID cannot be merged; the
command reports how many were skipped.

Every command saves the store immediately and re-renders the list.

## Sender rule

When a folder's emails are set (`setEmails` path), each email whose From
matches a rule and whose Message-ID is not yet stored is added to that
merge and the store is saved. Persisting on match keeps a later
`:unmerge` of a single email possible.

## Collapsing (`internal/ui/thread.go`)

New pure function, run after `flatEmails`/`threadEmails` and after the
existing in-memory filters (`/`, `z`):

```
collapseMerges(rows []threadedEmail, store *merge.Store, sortField, sortReverse) []threadedEmail
```

Rules:

1. A row is a member if its Message-ID is in a merge.
2. **Reply absorption**: if a member sits inside an automatic thread
   (rows sharing a thread in the `threadEmails` output), the whole thread
   joins the merge for display. Their Message-IDs are not written to the
   store; absorption is recomputed on every render.
3. All members of one merge are removed and replaced by a single
   collapsed row placed where its newest member would sort under the
   current sort field and direction.
4. A merge with one member in the folder still collapses, so the inbox
   stays consistent.
5. Threading disabled (`disableThreading`) still collapses merges;
   absorption then only applies to explicit members.

`threadedEmail` / `emailItem` gain a `merge *mergeRow` field:

```go
type mergeRow struct {
    title   string
    members []imap.Email // members present in the current list, newest first
}
```

## Rendering (`internal/ui/inbox.go`)

Collapsed row, columns unchanged in width:

- number, flag: `N` if any member is unread; `*` if all members marked.
- reply `·`: if any member is answered.
- thread connector column: `≡`.
- date, from, size, attachment `@`, spy `°`: from the newest member.
- subject: `<title> (<n>)` where n is the member count in this folder.

`FilterValue()` for the row is the title plus every member's From and
Subject, so `/` still finds it.

## Opening (`internal/ui/model.go`, `internal/ui/search.go`)

Enter, `l`, or `T` on a collapsed row opens the merge view. `T` on a
regular email keeps its current behaviour (conversation by subject and
participants).

- New `mergeResultMsg` and `fetchMergeCmd(title, ids)`. Folders searched:
  the same list `fetchConversationCmd` uses (Inbox, Sent, Archive,
  Waiting, Someday, Scheduled, Work, current).
- One IMAP `SEARCH` per folder with the criteria OR'd:
  `HEADER Message-ID <id>` and `HEADER In-Reply-To <id>` for every stored
  id (the second half fetches replies living in other folders). New
  client method `SearchByMessageIDs(ctx, folders, ids) ([]Email, error)`.
- Result view: `offTabFolder = "Merged: <title>"`, rows are plain
  (never collapsed inside the merge view), sorted by the active sort,
  subject prefixed with the folder like Thread view does. Enter on a
  member opens the reader unchanged. Esc returns to the folder.
- Reading a member marks it seen exactly as today; the collapsed row's
  `N` reflects it on return because members are re-read from `m.emails`.

## Actions on a collapsed row

`targetEmails()` returns the row's `members` when the cursor is on a
collapsed row and nothing is marked. `m` marks or unmarks all members.
Everything downstream (`A`, `x`, `M*`, `n`, `I`/`O`/`F`/`P`/`$`, undo)
works on the expanded list without change. Marked members that are
collapsed still count as marked; the collapsed row shows `*` when every
member is marked.

## Files touched

- `internal/merge/store.go`, `store_test.go` — new.
- `internal/config/config.go` — `MergesPath()`.
- `internal/ui/thread.go` — `collapseMerges`, `mergeRow`.
- `internal/ui/inbox.go` — render collapsed row, `FilterValue`,
  `setEmails` gains the store.
- `internal/ui/model.go` — store on the model, `targetEmails()`, Enter /
  `l` / `T` dispatch, `mergeResultMsg` handling, sender-rule application
  on load.
- `internal/ui/search.go` — `fetchMergeCmd`, `handleMergeResult`.
- `internal/ui/cmdline.go` — `:merge`, `:merge-sender`, `:unmerge`,
  completion.
- `internal/imap/client.go` — `SearchByMessageIDs`.
- `internal/ui/keys.go` — command-line rows and a note on Enter/`T`.
- `AGENTS.md`, `CHANGELOG.md`, `make docs`.

## Tests

- `internal/merge`: load/save round-trip, Add de-dup, Remove, Dissolve,
  MatchSender (case-insensitive substring, no rule → false), missing
  file → empty store, atomic save leaves no temp file.
- `internal/ui/thread_test.go` (new): `TestCollapseMerges_*` — basic
  collapse, sort position under each sort field, unread and answered
  aggregation, one-member merge, reply absorption pulls a whole
  automatic thread, two merges in one folder, threading disabled.
- `internal/ui/inbox_test.go`: collapsed row renders `≡`, title, count.
- `internal/ui/model_test.go`: `targetEmails()` expands a collapsed row;
  `m` on a collapsed row marks all members.
- `internal/ui/cmdline_test.go`: `:merge` with spaces in title,
  `:merge-sender` stores the rule, `:unmerge` inside and outside a merge
  view, skipped emails without Message-ID.
- Sender rule applied on load persists ids (`TestMergeSenderRuleOnLoad`).

## Invariants to re-check (AGENTS.md)

Threaded inbox rendering, `·` reply indicator, indicator columns, undo
of multi-move, screener priority on marked sets. Add a new "Merged
threads" entry with the test names above.
